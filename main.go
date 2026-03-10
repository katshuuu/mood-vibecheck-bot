package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var logFile *os.File

func initLogging() {
    var err error
    logFile, err = os.OpenFile("bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        log.Fatal("Ошибка создания лог-файла:", err)
    }
    log.SetOutput(logFile)
    log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

// Функция для логирования запросов к бэкенду
func logBackendRequest(endpoint string, payload interface{}, response interface{}, err error) {
    log.Printf("📤 Запрос к %s: %+v", endpoint, payload)
    if err != nil {
        log.Printf("❌ Ошибка: %v", err)
    } else {
        log.Printf("📥 Ответ: %+v", response)
    }
}

// Конфигурация
const (
	TELEGRAM_BOT_TOKEN = "8341440596:AAG6sTQLcOqvGMdNu3EN7bTbvKnj3FSIBjY"
	BACKEND_URL        = "http://localhost:3001" // URL вашего Node.js сервера
	PORT               = "8080"
	SITE_URL           = "http://localhost:3001" // URL вашего сайта
)

// Структуры данных
type TestSession struct {
	Step      int
	Scores    map[string]float64
	SessionID string // ID сессии из базы данных
	OrderID   string // ID заказа для отображения
}

type ResultPayload struct {
	TelegramID   int64              `json:"telegram_id"`
	TelegramName string             `json:"telegram_name"`
	Profile      map[string]string  `json:"profile"`
	Scores       map[string]float64 `json:"scores"`
	AIPrompt     string             `json:"ai_prompt"`
	SessionToken string             `json:"session_token"`
	Answers      map[string]string  `json:"answers"` // Сохраняем ответы на вопросы
}

// Хранилище сессий
var sessions = make(map[int64]*TestSession)

func main() {
	// Создаем бота
	bot, err := tgbotapi.NewBotAPI(TELEGRAM_BOT_TOKEN)
	if err != nil {
		log.Fatal("Ошибка создания бота:", err)
	}

	bot.Debug = false
	log.Printf("Бот @%s успешно запущен", bot.Self.UserName)

	// Запускаем HTTP сервер для проверки
	go startHTTPServer(bot)

	// Настраиваем получение обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// Обрабатываем обновления
	for update := range updates {
		if update.Message != nil {
			if update.Message.IsCommand() {
				handleCommand(bot, update.Message)
			} else if sessions[update.Message.Chat.ID] != nil {
				handleAnswer(bot, update.Message)
			}
		}
	}
}

func startHTTPServer(bot *tgbotapi.BotAPI) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "✅ AI Flower Test Bot is running!<br>Bot: @%s", bot.Self.UserName)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"bot":    bot.Self.UserName,
		})
	})

	log.Printf("HTTP сервер слушает порт %s", PORT)
	log.Fatal(http.ListenAndServe(":"+PORT, nil))
}

func handleCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatID := message.Chat.ID

	switch message.Command() {
	case "start":
		startTest(bot, chatID, message.From)
	case "help":
		sendHelp(bot, chatID)
	}
}

func startTest(bot *tgbotapi.BotAPI, chatID int64, user *tgbotapi.User) {
	// Создаем сессию на бэкенде
	sessionToken, orderID, err := createBackendSession(user.ID, user.UserName)
	if err != nil {
		log.Printf("Ошибка создания сессии: %v", err)
		send(bot, chatID, "😔 Произошла ошибка. Пожалуйста, попробуйте позже.")
		return
	}

	// Инициализируем локальную сессию
	sessions[chatID] = &TestSession{
		Step:      1,
		Scores:    initScores(),
		SessionID: sessionToken,
		OrderID:   orderID,
	}

	// Приветственное сообщение с ID заказа
	welcomeMsg := fmt.Sprintf(`✨ *you're the best,* пройди наш тест🫧

ответь на небольшие вопросы быстро, за 2 минуты

🆔 *ID заказа:* #%s

_ready?) поехали👇_`, orderID)

	msg := tgbotapi.NewMessage(chatID, welcomeMsg)
	msg.ParseMode = "Markdown"
	bot.Send(msg)

	// Отправляем первый вопрос
	sendQuestion(bot, chatID, 1)
}

func createBackendSession(telegramID int64, telegramName string) (string, string, error) {
    payload := map[string]interface{}{
        "telegramUserId":   fmt.Sprintf("%d", telegramID),
        "telegramUsername": telegramName,
    }

    jsonData, err := json.Marshal(payload)
    if err != nil {
        return "", "", err
    }

    log.Printf("📤 Создание сессии для пользователя %d (%s)", telegramID, telegramName)
    
    resp, err := http.Post(BACKEND_URL+"/api/create-session", "application/json", bytes.NewBuffer(jsonData))
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

    log.Printf("✅ Сессия создана: Token=%s, SessionID=%d", result.Token, result.SessionID)

    if !result.Success {
        return "", "", fmt.Errorf("backend error")
    }

    orderID := fmt.Sprintf("ORD-%d", time.Now().Unix()%10000)
    
    log.Printf("🔗 Ссылка для пользователя: %s", result.ShareURL)
    
    return result.Token, orderID, nil
}

func sendHelp(bot *tgbotapi.BotAPI, chatID int64) {
	helpText := "📋 **О тесте**\n\n" +
		"Этот тест поможет подобрать идеальный букет на основе ваших предпочтений.\n\n" +
		"Команды:\n" +
		"/start - начать тест\n" +
		"/help - это сообщение"

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
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
	chatID := message.Chat.ID
	answerText := message.Text

	session := sessions[chatID]
	if session == nil {
		return
	}

	// Преобразуем текст ответа в код
	answerCode := textToCode(session.Step, answerText)
	if answerCode == "" {
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите один из предложенных вариантов, нажав на кнопку с ответом.")
		bot.Send(msg)
		return
	}

	// Сохраняем ответ
	applyAnswer(session, answerCode)
	session.Step++

	if session.Step > 7 {
		finishTest(bot, chatID, session)
		delete(sessions, chatID)
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
	bot.Send(msg)

	// Отправляем фото
	var mediaGroup []interface{}
	for i := 0; i < 4; i++ {
		photo := tgbotapi.NewInputMediaPhoto(tgbotapi.FilePath(photoPaths[i]))
		mediaGroup = append(mediaGroup, photo)
	}

	if _, err := bot.SendMediaGroup(tgbotapi.NewMediaGroup(chatID, mediaGroup)); err != nil {
		log.Printf("Ошибка отправки медиа-группы: %v", err)
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

	promptMsg := tgbotapi.NewMessage(chatID, "Выберите вариант ответа:")
	promptMsg.ReplyMarkup = keyboard
	if _, err := bot.Send(promptMsg); err != nil {
		log.Printf("Ошибка отправки клавиатуры: %v", err)
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
	// Определяем максимальные категории
	color := maxCategory(s.Scores, []string{"P", "B", "D", "N"})
	form := maxCategory(s.Scores, []string{"R", "A", "C", "M"})
	flower := maxCategory(s.Scores, []string{"F1", "F2", "F3", "F4"})
	mood := maxCategory(s.Scores, []string{"M1", "M2", "M3", "M4"})

	profile := map[string]string{
		"color":  color,
		"form":   form,
		"flower": flower,
		"mood":   mood,
	}

	aiPrompt := generateAIPrompt(profile)

	// Сохраняем результаты в базу данных
    err := saveResultsToBackend(chatID, user.UserName, s, profile, aiPrompt, nil) // Добавьте answers если есть
    if err != nil {
        log.Printf("Ошибка сохранения результатов: %v", err)
    }
	// Отправляем ссылку на браузерный чат
	shareURL := fmt.Sprintf("%s/quiz/%s", SITE_URL, s.SessionID)
	chatLink := fmt.Sprintf("%s/chat/%s", SITE_URL, s.SessionID)

	resultText := fmt.Sprintf(`✨ **Ваш профиль готов!** ✨

🆔 *ID заказа:* #%s

🌺 **Тип:** %s
🎨 **Цвет:** %s
📐 **Форма:** %s
🌸 **Настроение:** %s

🔗 **Ваша индивидуальная ссылка:**
%s

💬 **Перейдите в браузер, чтобы увидеть ваш уникальный букет и пообщаться с AI-ассистентом:**
%s

_Там вы сможете посмотреть генерацию и оформить заказ_`,
		s.OrderID,
		getMoodName(mood),
		getColorName(color),
		getFormName(form),
		getFlowerName(flower),
		shareURL,
		chatLink)

	msg := tgbotapi.NewMessage(chatID, resultText)
	msg.ParseMode = "Markdown"

	// Убираем клавиатуру
	hideKeyboard := tgbotapi.NewRemoveKeyboard(true)
	msg.ReplyMarkup = hideKeyboard
	bot.Send(msg)

	// Отправляем промпт
	promptMsg := tgbotapi.NewMessage(chatID, "🤖 *AI Prompt для генерации:*\n```\n"+aiPrompt+"\n```")
	promptMsg.ParseMode = "Markdown"
	bot.Send(promptMsg)
}

func saveResultsToBackend(chatID int64, telegramName string, s *TestSession, profile map[string]string, aiPrompt string, answers map[string]string) error {
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

    log.Printf("📤 Отправка результатов теста для сессии %s", s.SessionID)
    log.Printf("📊 Профиль: color=%s, form=%s, mood=%s", profile["color"], profile["form"], profile["mood"])
    
    resp, err := http.Post(BACKEND_URL+"/api/save-test-results", "application/json", bytes.NewBuffer(jsonData))
    if err != nil {
        log.Printf("❌ Ошибка отправки результатов: %v", err)
        return err
    }
    defer resp.Body.Close()

    var response struct {
        Success   bool   `json:"success"`
        Message   string `json:"message"`
        RequestID string `json:"requestId"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
        return err
    }

    if response.Success {
        log.Printf("✅ Результаты успешно сохранены! RequestID: %s", response.RequestID)
    } else {
        log.Printf("⚠️ Сервер вернул ошибку: %s", response.Message)
    }

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("backend вернул статус: %s", resp.Status)
    }

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

func getFormName(code string) string {
	names := map[string]string{
		"R": "Классическая гармония",
		"A": "Современный динамизм",
		"C": "Плавная текучесть",
		"M": "Минималистичная ясность",
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

func getFlowerName(code string) string {
	names := map[string]string{
		"F1": "Нежность",
		"F2": "Страсть",
		"F3": "Загадка",
		"F4": "Спокойствие",
	}
	return names[code]
}

func send(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки сообщения: %v", err)
	}
}
