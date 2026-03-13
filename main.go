package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Глобальные переменные для конфигурации
var (
	backendURL string
	siteURL    string
	logFile    *os.File
	startTime  = time.Now()
	shutdownCh = make(chan struct{})
)

// HTTP клиент с таймаутами
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// Структуры данных
type TestSession struct {
	Step      int
	Scores    map[string]float64
	SessionID string
	OrderID   string
	CreatedAt time.Time
	Answers   map[string]string // Сохраняем сырые ответы
}

type ResultPayload struct {
	TelegramID   int64              `json:"telegram_id"`
	TelegramName string             `json:"telegram_name"`
	Profile      map[string]string  `json:"profile"`
	Scores       map[string]float64 `json:"scores"`
	AIPrompt     string             `json:"ai_prompt"`
	SessionToken string             `json:"session_token"`
	Answers      map[string]string  `json:"answers"`
}

// Хранилище сессий с мьютексом для потокобезопасности
var (
	sessions      = make(map[int64]*TestSession)
	sessionsMutex = &sync.RWMutex{}
)

func init() {
	// Инициализация конфигурации из переменных окружения
	backendURL = os.Getenv("BACKEND_URL")
	if backendURL == "" {
		backendURL = "http://localhost:3001" // значение по умолчанию для разработки
	}

	siteURL = os.Getenv("SITE_URL")
	if siteURL == "" {
		siteURL = "http://localhost:3001" // значение по умолчанию для разработки
	}
}

func initLogging() {
	var err error
	logFile, err = os.OpenFile("bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal("❌ Ошибка создания лог-файла:", err)
	}
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func main() {
	// Инициализация логирования
	initLogging()
	defer logFile.Close()

	// Проверяем наличие папки с фото
	if _, err := os.Stat("photos"); os.IsNotExist(err) {
		log.Println("📁 Папка 'photos' не найдена, создаем...")
		os.Mkdir("photos", 0755)
		log.Println("✅ Папка 'photos' создана")
	}

	// Читаем токен из переменных окружения
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("❌ TELEGRAM_BOT_TOKEN не установлен")
	}

	// Читаем порт из переменных окружения
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Создаем бота
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal("❌ Ошибка создания бота:", err)
	}

	// Устанавливаем режим отладки из переменной окружения
	debug := os.Getenv("DEBUG") == "true"
	bot.Debug = debug

	log.Printf("✅ Бот @%s успешно запущен", bot.Self.UserName)
	log.Printf("🌐 Backend URL: %s", backendURL)
	log.Printf("🌍 Site URL: %s", siteURL)
	log.Printf("🔧 Debug mode: %v", debug)

	// Запускаем HTTP сервер для проверки
	go startHTTPServer(bot, port)

	// Запускаем горутину для очистки старых сессий
	go cleanOldSessions()

	// Настраиваем graceful shutdown
	setupGracefulShutdown()

	// Настраиваем получение обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	log.Println("🤖 Бот начал прослушивание обновлений...")

	// Обрабатываем обновления
	for update := range updates {
		if update.Message != nil {
			// Защита от спама - игнорируем старые сообщения
			if update.Message.Time().Before(time.Now().Add(-60 * time.Second)) {
				continue
			}

			// Используем мьютекс для безопасного доступа к сессиям
			sessionsMutex.RLock()
			session := sessions[update.Message.Chat.ID]
			sessionsMutex.RUnlock()

			if update.Message.IsCommand() {
				handleCommand(bot, update.Message)
			} else if session != nil {
				handleAnswer(bot, update.Message)
			}
		}
	}
}

func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("🛑 Получен сигнал завершения, останавливаем бота...")

		// Сохраняем информацию о сессиях перед выходом
		sessionsMutex.RLock()
		log.Printf("📊 Активных сессий: %d", len(sessions))
		sessionsMutex.RUnlock()

		// Сигнал для HTTP сервера
		close(shutdownCh)

		log.Println("👋 Бот остановлен")
		os.Exit(0)
	}()
}

func cleanOldSessions() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		sessionsMutex.Lock()
		for chatID, session := range sessions {
			// Очищаем сессии старше 24 часов
			if time.Since(session.CreatedAt) > 24*time.Hour {
				delete(sessions, chatID)
				log.Printf("🧹 Удалена старая сессия для чата %d", chatID)
			}
		}
		sessionsMutex.Unlock()
	}
}

func startHTTPServer(bot *tgbotapi.BotAPI, port string) {
	mux := http.NewServeMux()

	// Основной обработчик
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		html := `<!DOCTYPE html>
<html>
<head>
    <title>Mood VibeCheck Bot</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            margin: 40px;
            text-align: center;
            background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
            color: white;
        }
        .container {
            background: rgba(255, 255, 255, 0.1);
            border-radius: 10px;
            padding: 20px;
            max-width: 600px;
            margin: 0 auto;
            backdrop-filter: blur(10px);
            box-shadow: 0 8px 32px 0 rgba(31, 38, 135, 0.37);
        }
        .status {
            display: inline-block;
            padding: 5px 15px;
            background: #4CAF50;
            color: white;
            border-radius: 20px;
            font-weight: bold;
        }
        .info {
            margin-top: 20px;
            font-size: 14px;
            color: #e0e0e0;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🌸 AI Flower Test Bot</h1>
        <div class="status">✅ ONLINE</div>
        <p>Бот: <strong>@%s</strong></p>
        <p>Backend: %s</p>
        <div class="info">
            Сервер работает в штатном режиме<br>
            Время запуска: %s
        </div>
    </div>
</body>
</html>`
		fmt.Fprintf(w, html, bot.Self.UserName, backendURL, time.Now().Format("02.01.2006 15:04:05"))
	})

	// Health check для мониторинга
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		sessionsMutex.RLock()
		activeSessions := len(sessions)
		sessionsMutex.RUnlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"bot":            bot.Self.UserName,
			"time":           time.Now().Unix(),
			"backend":        backendURL,
			"site":           siteURL,
			"sessions":       activeSessions,
			"uptime_seconds": time.Since(startTime).Seconds(),
		})
	})

	// Метрики для мониторинга
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")

		sessionsMutex.RLock()
		activeSessions := len(sessions)
		sessionsMutex.RUnlock()

		fmt.Fprintf(w, "# HELP bot_active_sessions Active test sessions\n")
		fmt.Fprintf(w, "# TYPE bot_active_sessions gauge\n")
		fmt.Fprintf(w, "bot_active_sessions %d\n", activeSessions)
		fmt.Fprintf(w, "bot_uptime_seconds %f\n", time.Since(startTime).Seconds())
	})

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("🌐 HTTP сервер запущен на порту %s", port)
	log.Printf("📊 Health check: http://localhost:%s/health", port)
	log.Printf("📈 Metrics: http://localhost:%s/metrics", port)

	// Запускаем сервер в горутине
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("⚠️ HTTP сервер ошибка: %v", err)
		}
	}()

	// Ждем сигнала завершения
	<-shutdownCh
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("⚠️ Ошибка при остановке HTTP сервера: %v", err)
	}
}

func handleCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatID := message.Chat.ID
	commandText := message.Text

	// Разбираем команду
	parts := strings.Split(commandText, " ")
	command := strings.ToLower(strings.TrimPrefix(parts[0], "/"))

	var token string
	if len(parts) > 1 {
		token = parts[1]
	}

	switch command {
	case "start":
		startTest(bot, chatID, message.From, token)
	case "help":
		sendHelp(bot, chatID)
	default:
		send(bot, chatID, "❌ Неизвестная команда. Используйте /help для списка команд.")
	}
}

func startTest(bot *tgbotapi.BotAPI, chatID int64, user *tgbotapi.User, tokenFromURL string) {
	if tokenFromURL == "" {
		log.Printf("❌ ОШИБКА: Нет токена в команде /start")
		msg := tgbotapi.NewMessage(chatID, "❌ Ошибка: неверная ссылка. Начните тест с сайта.")
		bot.Send(msg)
		return
	}

	// Проверяем, существует ли сессия на бэкенде
	exists, err := checkSessionExists(tokenFromURL)
	if err != nil {
		log.Printf("⚠️ Ошибка проверки сессии: %v", err)
	}

	if !exists {
		// Если сессия не существует, создаем её
		log.Printf("📝 Сессия %s не найдена, создаем на бэкенде", tokenFromURL)
		_, _, err := createBackendSessionWithToken(user.ID, user.UserName, tokenFromURL)
		if err != nil {
			log.Printf("❌ Ошибка создания сессии на бэкенде: %v", err)
		}
	}

	sessionToken := tokenFromURL

	sessionsMutex.Lock()
	sessions[chatID] = &TestSession{
		Step:      1,
		Scores:    initScores(),
		SessionID: sessionToken,
		CreatedAt: time.Now(),
		Answers:   make(map[string]string),
	}
	sessionsMutex.Unlock()

	log.Printf("📝 Новая сессия создана для пользователя %d, токен: %s", user.ID, sessionToken)

	// Приветственное сообщение
	welcomeMsg := "✨ *you're the best,* пройди наш тест🫧\n\nответь на небольшие вопросы быстро, за 2 минуты\n\n_ready?) поехали👇_"

	msg := tgbotapi.NewMessage(chatID, welcomeMsg)
	msg.ParseMode = "Markdown"
	bot.Send(msg)

	sendQuestion(bot, chatID, 1)
}

func createBackendSessionWithToken(telegramID int64, telegramName string, token string) (string, string, error) {
	payload := map[string]interface{}{
		"telegramUserId":   fmt.Sprintf("%d", telegramID),
		"telegramUsername": telegramName,
		"token":            token,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}

	log.Printf("📤 Создание сессии с токеном %s для пользователя %d", token, telegramID)

	resp, err := httpClient.Post(backendURL+"/api/create-session-with-token", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ Ошибка создания сессии: %v", err)
		return "", "", err
	}
	defer resp.Body.Close()

	var result struct {
		Success   bool   `json:"success"`
		Token     string `json:"token"`
		SessionID int    `json:"sessionId"`
		ShareURL  string `json:"shareUrl"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}

	if !result.Success {
		return "", "", fmt.Errorf("backend вернул success=false")
	}

	return result.Token, fmt.Sprintf("ORD-%d", time.Now().Unix()%10000), nil
}

func sendHelp(bot *tgbotapi.BotAPI, chatID int64) {
	helpText := `📋 *О тесте*

Этот тест поможет подобрать идеальный букет на основе ваших предпочтений.

*Команды:*
/start - начать тест
/help - это сообщение

*Как это работает:*
1. Ответьте на 7 простых вопросов
2. Получите ваш уникальный профиль
3. Перейдите по ссылке для просмотра букета`

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	if _, err := bot.Send(msg); err != nil {
		log.Printf("❌ Ошибка отправки help: %v", err)
	}
}

func initScores() map[string]float64 {
	return map[string]float64{
		"P": 0, "B": 0, "D": 0, "N": 0,
		"R": 0, "A": 0, "C": 0, "M": 0,
		"F1": 0, "F2": 0, "F3": 0, "F4": 0,
		"M1": 0, "M2": 0, "M3": 0, "M4": 0,
	}
}

func handleAnswer(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ PANIC в handleAnswer: %v", r)
			msg := tgbotapi.NewMessage(message.Chat.ID, "😔 Произошла внутренняя ошибка. Пожалуйста, начните тест заново с /start")
			bot.Send(msg)
		}
	}()

	chatID := message.Chat.ID
	answerText := message.Text
	user := message.From

	log.Printf("🔍 ПОЛУЧЕН ОТВЕТ: chatID=%d, text='%s'", chatID, answerText)

	sessionsMutex.RLock()
	session := sessions[chatID]
	sessionsMutex.RUnlock()

	if session == nil {
		log.Printf("⚠️ Сессия не найдена для чата %d", chatID)
		msg := tgbotapi.NewMessage(chatID, "❌ Сессия не найдена. Начните тест заново с помощью /start")
		bot.Send(msg)
		return
	}

	// Преобразуем текст ответа в код
	answerCode := textToCode(session.Step, answerText)
	if answerCode == "" {
		log.Printf("⚠️ Неизвестный ответ на шаге %d: %s", session.Step, answerText)
		msg := tgbotapi.NewMessage(chatID, "❌ Пожалуйста, выберите один из предложенных вариантов, нажав на кнопку с ответом.")
		bot.Send(msg)
		return
	}

	log.Printf("📊 Пользователь %d ответил на вопрос %d: %s -> %s", user.ID, session.Step, answerText, answerCode)

	// Сохраняем ответ
	session.Answers[fmt.Sprintf("q%d", session.Step)] = answerCode
	applyAnswer(session, answerCode)

	session.Step++

	if session.Step > 7 {
		finishTest(bot, chatID, session, user)

		sessionsMutex.Lock()
		delete(sessions, chatID)
		sessionsMutex.Unlock()
		return
	}

	sendQuestion(bot, chatID, session.Step)
}

func textToCode(step int, text string) string {
	switch step {
	case 1:
		switch text {
		case "Весна 🌸":
			return "spring"
		case "Лето ☀️":
			return "summer"
		case "Осень 🍂":
			return "autumn"
		case "Зима ❄️":
			return "winter"
		}
	case 2:
		switch text {
		case "Пастельные тона 🌸":
			return "pastel"
		case "Яркие краски 🌈":
			return "bright"
		case "Глубокие оттенки 🌑":
			return "dark"
		case "Натуральные цвета 🌿":
			return "natural"
		}
	case 3:
		switch text {
		case "У воды 🌊":
			return "water"
		case "В лесу 🌲":
			return "forest"
		case "В городе 🏙":
			return "city"
		case "Дома 🏡":
			return "home"
		}
	case 4:
		switch text {
		case "Плавные линии ⭕️":
			return "round"
		case "Асимметрия 🔷":
			return "asym"
		case "Волны 🌊":
			return "wave"
		case "Минимализм ▫️":
			return "minimal"
		}
	case 5:
		switch text {
		case "Дружок⚡️":
			return "krosh"
		case "Роза🌸":
			return "piglet"
		case "Малыш🐣":
			return "tigger"
		case "Гена📚":
			return "owl"
		}
	case 6:
		switch text {
		case "1":
			return "philo"
		case "2":
			return "chaos"
		case "3":
			return "romantic"
		case "4":
			return "sarcasm"
		}
	case 7:
		switch text {
		case "Утро 🌅":
			return "morning"
		case "День ☀️":
			return "day"
		case "Вечер 🌆":
			return "evening"
		case "Ночь 🌙":
			return "night"
		}
	}
	return ""
}

func sendQuestion(bot *tgbotapi.BotAPI, chatID int64, step int) {
	var text string
	var options []string
	var photoPaths []string

	switch step {
	case 1:
		text = " _выбери любимое время года:_"
		options = []string{"Весна 🌸", "Лето ☀️", "Осень 🍂", "Зима ❄️"}
		photoPaths = []string{"photos/spring.jpg", "photos/summer.jpg", "photos/autumn.jpg", "photos/winter.jpg"}
	case 2:
		text = " _какое цветовое сочетание тебе ближе?_ "
		options = []string{"Пастельные тона 🌸", "Яркие краски 🌈", "Глубокие оттенки 🌑", "Натуральные цвета 🌿"}
		photoPaths = []string{"photos/pastel.jpg", "photos/bright.jpg", "photos/dark.jpg", "photos/natural.jpg"}
	case 3:
		text = " _где ты чувствуешь спокойствие?_ "
		options = []string{"У воды 🌊", "В лесу 🌲", "В городе 🏙", "Дома 🏡"}
		photoPaths = []string{"photos/water.jpg", "photos/forest.jpg", "photos/city.jpg", "photos/home.jpg"}
	case 4:
		text = " _какая форма нравится больше остальных?_ "
		options = []string{"Плавные линии ⭕️", "Асимметрия 🔷", "Волны 🌊", "Минимализм ▫️"}
		photoPaths = []string{"photos/round.jpg", "photos/asym.jpg", "photos/wave.jpg", "photos/mini.jpg"}
	case 5:
		text = " _а какой ты Барбоскин?_ "
		options = []string{"Дружок⚡️", "Роза🌸", "Малыш🐣", "Гена📚"}
		photoPaths = []string{"photos/krosh.jpg", "photos/piglet.jpg", "photos/tigger.jpg", "photos/owl.jpg"}
	case 6:
		text = " _какой мем тебе ближе?)_ "
		options = []string{"1", "2", "3", "4"}
		photoPaths = []string{"photos/philo.jpg", "photos/chaos.jpg", "photos/romantic.jpg", "photos/sarcasm.jpg"}
	case 7:
		text = " _в какое время ты наиболее активен?_ "
		options = []string{"Утро 🌅", "День ☀️", "Вечер 🌆", "Ночь 🌙"}
		photoPaths = []string{"photos/morning.jpg", "photos/day.jpg", "photos/evening.jpg", "photos/night.jpg"}
	}

	// Отправляем вопрос
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := bot.Send(msg); err != nil {
		log.Printf("❌ Ошибка отправки вопроса: %v", err)
	}

	// Пытаемся отправить фото
	var mediaGroup []interface{}
	photosExist := false

	for _, path := range photoPaths {
		if file, err := os.Stat(path); err == nil && !file.IsDir() {
			photo := tgbotapi.NewInputMediaPhoto(tgbotapi.FilePath(path))
			mediaGroup = append(mediaGroup, photo)
			photosExist = true
		} else {
			log.Printf("📷 Фото не найдено: %s", path)
		}
	}

	if photosExist && len(mediaGroup) > 0 {
		if _, err := bot.SendMediaGroup(tgbotapi.NewMediaGroup(chatID, mediaGroup)); err != nil {
			log.Printf("⚠️ Не удалось отправить медиа-группу: %v", err)
		}
	}

	// Создаем клавиатуру
	keyboard := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(options[0]),
			tgbotapi.NewKeyboardButton(options[1]),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(options[2]),
			tgbotapi.NewKeyboardButton(options[3]),
		),
	)

	promptMsg := tgbotapi.NewMessage(chatID, "👇 Выберите вариант ответа:")
	promptMsg.ReplyMarkup = keyboard
	if _, err := bot.Send(promptMsg); err != nil {
		log.Printf("❌ Ошибка отправки клавиатуры: %v", err)
	}
}

func applyAnswer(s *TestSession, a string) {
	switch a {
	case "spring":
		s.Scores["P"] += 1.5
		s.Scores["M1"] += 1
	case "summer":
		s.Scores["B"] += 1.5
		s.Scores["M2"] += 1
	case "autumn":
		s.Scores["D"] += 1.5
		s.Scores["M3"] += 1
	case "winter":
		s.Scores["N"] += 1.5
		s.Scores["M4"] += 1
	case "pastel":
		s.Scores["P"] += 2
		s.Scores["M1"] += 0.5
	case "bright":
		s.Scores["B"] += 2
		s.Scores["M2"] += 0.5
	case "dark":
		s.Scores["D"] += 2
		s.Scores["M3"] += 0.5
	case "natural":
		s.Scores["N"] += 2
		s.Scores["M4"] += 0.5
	case "water":
		s.Scores["R"] += 1.5
	case "forest":
		s.Scores["C"] += 1.5
	case "city":
		s.Scores["A"] += 1.5
	case "home":
		s.Scores["M"] += 1.5
	case "round":
		s.Scores["R"] += 1.5
	case "asym":
		s.Scores["A"] += 1.5
	case "wave":
		s.Scores["C"] += 1.5
	case "minimal":
		s.Scores["M"] += 1.5
	case "krosh":
		s.Scores["B"] += 1
		s.Scores["F2"] += 1
	case "piglet":
		s.Scores["P"] += 1
		s.Scores["F1"] += 1
	case "tigger":
		s.Scores["B"] += 1
		s.Scores["F2"] += 1
	case "owl":
		s.Scores["N"] += 1
		s.Scores["F4"] += 1
	case "philo":
		s.Scores["N"] += 0.5
		s.Scores["M4"] += 1
	case "chaos":
		s.Scores["B"] += 0.5
		s.Scores["M2"] += 1
	case "romantic":
		s.Scores["P"] += 0.5
		s.Scores["M1"] += 1
	case "sarcasm":
		s.Scores["D"] += 0.5
		s.Scores["M3"] += 1
	case "morning":
		s.Scores["P"] += 1
		s.Scores["M1"] += 0.5
	case "day":
		s.Scores["B"] += 1
		s.Scores["M2"] += 0.5
	case "evening":
		s.Scores["D"] += 1
		s.Scores["M3"] += 0.5
	case "night":
		s.Scores["N"] += 1
		s.Scores["M4"] += 0.5
	}
}

func finishTest(bot *tgbotapi.BotAPI, chatID int64, s *TestSession, user *tgbotapi.User) {
	log.Printf("🏁 Завершение теста для пользователя %d, токен: %s", user.ID, s.SessionID)

	// Определяем профиль
	color := maxCategory(s.Scores, []string{"P", "B", "D", "N"})
	form := maxCategory(s.Scores, []string{"R", "A", "C", "M"})
	mood := maxCategory(s.Scores, []string{"M1", "M2", "M3", "M4"})

	log.Printf("📊 Результаты: color=%s, form=%s, mood=%s", color, form, mood)

	profile := map[string]string{
		"color": color,
		"form":  form,
		"mood":  mood,
	}

	aiPrompt := generateAIPrompt(profile)
	log.Printf("📝 AI Prompt сгенерирован")

	// Отправляем результаты на сервер
	err := saveResultsToBackend(user.ID, user.UserName, s, profile, aiPrompt, s.Answers)
	if err != nil {
		log.Printf("❌ Ошибка сохранения результатов: %v", err)
		// Отправляем сообщение об ошибке пользователю
		errorMsg := tgbotapi.NewMessage(chatID, "❌ Произошла ошибка при сохранении результатов. Пожалуйста, попробуйте позже.")
		bot.Send(errorMsg)
		return
	}

	// Сообщение пользователю
	resultText := fmt.Sprintf(`_Спасибо за прохождение теста_✨

🌺 *Ваш тип личности:* %s
🎨 *Цветовая гамма:* %s`,
		getMoodName(mood),
		getColorName(color))

	msg := tgbotapi.NewMessage(chatID, resultText)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)

	if _, err := bot.Send(msg); err != nil {
		log.Printf("❌ Ошибка отправки результата пользователю: %v", err)
	} else {
		log.Printf("✅ Тест успешно завершен для пользователя %d", user.ID)
	}
}

func saveResultsToBackend(chatID int64, telegramName string, s *TestSession, profile map[string]string, aiPrompt string, answers map[string]string) error {
	if s.SessionID == "" {
		return fmt.Errorf("empty session token")
	}

	// Подготавливаем данные
	payload := ResultPayload{
		TelegramID:   chatID,
		TelegramName: telegramName,
		Profile:      profile,
		Scores:       s.Scores,
		AIPrompt:     aiPrompt,
		SessionToken: s.SessionID,
		Answers:      answers,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := backendURL + "/api/save-test-results"
	log.Printf("📤 Отправка результатов на %s для сессии %s", url, s.SessionID)

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("📥 Ответ от сервера: %s", string(body))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var response struct {
		Success bool `json:"success"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}

	if !response.Success {
		return fmt.Errorf("server returned success=false")
	}

	log.Printf("✅ Результаты успешно сохранены на сервере")
	return nil
}

func generateAIPrompt(profile map[string]string) string {
	colorMap := map[string]string{
		"P": "pastel pink and soft peach tones with romantic delicate flowers",
		"B": "bright coral, yellow and turquoise tones with exotic dynamic flowers",
		"D": "deep burgundy, plum and chocolate tones with dramatic lush flowers",
		"N": "neutral beige, cream and muted green tones with natural wild flowers",
	}

	formMap := map[string]string{
		"R": "round and balanced composition in classic style",
		"A": "asymmetrical modern composition with dynamic lines",
		"C": "cascading flowing composition with graceful curves",
		"M": "minimalistic clean composition with negative space",
	}

	moodMap := map[string]string{
		"M1": "soft romantic mood with gentle atmosphere",
		"M2": "bright joyful mood with energetic vibe",
		"M3": "deep dramatic mood with mysterious aura",
		"M4": "calm aesthetic mood with peaceful harmony",
	}

	color := colorMap[profile["color"]]
	form := formMap[profile["form"]]
	mood := moodMap[profile["mood"]]

	return fmt.Sprintf(
		"Create a premium artistic flower bouquet with %s, %s, %s. "+
			"Ultra realistic photography, soft natural lighting, luxury floral design, "+
			"high detail, editorial style, 4k, professional flower arrangement, "+
			"bokeh background, award-winning photography",
		color, form, mood,
	)
}

func maxCategory(scores map[string]float64, keys []string) string {
	maxVal := -1.0
	maxKey := keys[0]

	for _, k := range keys {
		if scores[k] > maxVal {
			maxVal = scores[k]
			maxKey = k
		}
	}

	return maxKey
}

func getColorName(code string) string {
	names := map[string]string{
		"P": "Нежная пастель",
		"B": "Яркая энергия",
		"D": "Глубокая драма",
		"N": "Природная гармония",
	}
	return names[code]
}

func getMoodName(code string) string {
	names := map[string]string{
		"M1": "Романтик",
		"M2": "Оптимист",
		"M3": "Интеллектуал",
		"M4": "Философ",
	}
	return names[code]
}

func send(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("❌ Ошибка отправки сообщения: %v", err)
	}
}

func checkSessionExists(token string) (bool, error) {
	resp, err := httpClient.Get(backendURL + "/api/check-session/" + token)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var result struct {
		Exists bool `json:"exists"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}

	return result.Exists, nil
}
