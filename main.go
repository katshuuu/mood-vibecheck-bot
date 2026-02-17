package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Константа с токеном бота - ЗАМЕНИТЕ НА СВОЙ ТОКЕН!
const TELEGRAM_BOT_TOKEN = "8341440596:AAG6sTQLcOqvGMdNu3EN7bTbvKnj3FSIBjY"

// URL бэкенда (если есть, иначе оставьте пустым)
const BACKEND_URL = ""

// Порт для HTTP сервера
const PORT = "8080"

type TestSession struct {
	Step   int
	Scores map[string]float64
}

type ResultPayload struct {
	TelegramID int64              `json:"telegram_id"`
	Profile    map[string]string  `json:"profile"`
	Scores     map[string]float64 `json:"scores"`
	AIPrompt   string             `json:"ai_prompt"`
}

var sessions = make(map[int64]*TestSession)

func main() {
	// Используем токен из константы
	token := TELEGRAM_BOT_TOKEN
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не установлен в коде!")
	}

	backendURL := BACKEND_URL
	port := PORT

	// Создаем бота
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal("Ошибка создания бота:", err)
	}

	bot.Debug = false // Включите true для отладки
	log.Printf("Бот @%s успешно запущен", bot.Self.UserName)

	// Запускаем HTTP сервер для проверки работоспособности
	go func() {
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

		log.Printf("HTTP сервер слушает порт %s", port)
		log.Fatal(http.ListenAndServe(":"+port, nil))
	}()

	// Настраиваем получение обновлений
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// Обрабатываем обновления
	for update := range updates {
		if update.Message != nil && update.Message.IsCommand() {
			handleCommand(bot, update.Message)
		}

		if update.Message != nil && !update.Message.IsCommand() && sessions[update.Message.Chat.ID] != nil {
			handleAnswer(bot, update.Message, backendURL)
		}
	}
}

func handleCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatID := message.Chat.ID

	switch message.Command() {
	case "start":
		startTest(bot, chatID)
	case "help":
		sendHelp(bot, chatID)
	}
}

func startTest(bot *tgbotapi.BotAPI, chatID int64) {
	// Приветственное сообщение с легендой
	welcomeMsg := "_you’re the best,_ **пройди наш тест🫧**\n\n" +
		"ответь на небольшие вопросы быстро,\n" +
		"за 2 минуты\n\n" +
		"_ready?) поехали👇_"

	msg := tgbotapi.NewMessage(chatID, welcomeMsg)
	msg.ParseMode = "Markdown"
	bot.Send(msg)

	// Инициализируем сессию
	sessions[chatID] = &TestSession{
		Step:   1,
		Scores: initScores(),
	}

	// Отправляем первый вопрос с фото
	sendQuestion(bot, chatID, 1)
}

func sendHelp(bot *tgbotapi.BotAPI, chatID int64) {
	helpText := "📋 **О тесте**\n\n" +
		"Этот тест поможет определить ваш психологический тип на основе визуальных предпочтений.\n\n" +
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

func handleAnswer(bot *tgbotapi.BotAPI, message *tgbotapi.Message, backendURL string) {
	chatID := message.Chat.ID
	answerText := message.Text

	session := sessions[chatID]
	if session == nil {
		return
	}

	// Преобразуем текст ответа в код для applyAnswer
	answerCode := textToCode(session.Step, answerText)
	if answerCode == "" {
		// Если текст не соответствует ожидаемому, просим выбрать из вариантов
		msg := tgbotapi.NewMessage(chatID, "Пожалуйста, выберите один из предложенных вариантов, нажав на кнопку с ответом.")
		bot.Send(msg)
		return
	}

	applyAnswer(session, answerCode)
	session.Step++

	if session.Step > 7 {
		finish(bot, chatID, session, backendURL)
		delete(sessions, chatID)
		return
	}

	sendQuestion(bot, chatID, session.Step)
}

func textToCode(step int, text string) string {
	// Маппинг текста ответов в коды для каждого вопроса
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

	// Отправляем вопрос с текстом
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	bot.Send(msg)

	// Создаем медиа-группу из 4 фото БЕЗ подписей
	var mediaGroup []interface{}
	for i := 0; i < 4; i++ {
		photo := tgbotapi.NewInputMediaPhoto(tgbotapi.FilePath(photoPaths[i]))
		// Не устанавливаем Caption, чтобы фото были без подписей
		mediaGroup = append(mediaGroup, photo)
	}

	// Отправляем все 4 фото одним сообщением (медиа-группой)
	if _, err := bot.SendMediaGroup(tgbotapi.NewMediaGroup(chatID, mediaGroup)); err != nil {
		log.Printf("Ошибка отправки медиа-группы: %v", err)
	}

	// Создаем клавиатуру с 4 кнопками
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

	// Отправляем сообщение с клавиатурой (без дополнительного текста)
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

	case "water", "forest", "city", "home":
		// Для вопроса 3
		switch a {
		case "water":
			s.Scores["R"] += 1.5
		case "forest":
			s.Scores["C"] += 1.5
		case "city":
			s.Scores["A"] += 1.5
		case "home":
			s.Scores["M"] += 1.5
		}

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

func finish(bot *tgbotapi.BotAPI, chatID int64, s *TestSession, backendURL string) {
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

	// Отправляем результаты пользователю
	resultText := fmt.Sprintf(`✨ **Ваш психологический профиль готов!** ✨

🌺 **Тип личности:** %s
🎨 **Цветовая энергия:** %s
📐 **Форма мышления:** %s
🌸 **Эмоциональный фон:** %s

Спасибо за участие в исследовании!`,
		getMoodName(mood),
		getColorName(color),
		getFormName(form),
		getFlowerName(flower))

	msg := tgbotapi.NewMessage(chatID, resultText)
	msg.ParseMode = "Markdown"

	// Убираем клавиатуру после завершения теста
	hideKeyboard := tgbotapi.NewRemoveKeyboard(true)
	msg.ReplyMarkup = hideKeyboard
	bot.Send(msg)

	payload := ResultPayload{
		TelegramID: chatID,
		Profile:    profile,
		Scores:     s.Scores,
		AIPrompt:   aiPrompt,
	}

	if backendURL != "" {
		sendToBackend(backendURL, payload)
	}

	// Показываем промпт (можно закомментировать если не нужно)
	promptMsg := tgbotapi.NewMessage(chatID, "🔮 *AI Prompt:*\n```\n"+aiPrompt+"\n```")
	promptMsg.ParseMode = "Markdown"
	bot.Send(promptMsg)
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

	prompt := fmt.Sprintf(
		"Create a premium artistic flower bouquet with %s, %s, %s. "+
			"Ultra realistic photography, soft natural lighting, luxury floral design, "+
			"high detail, editorial style, 4k, professional flower arrangement, "+
			"bokeh background, award-winning photography",
		color, form, mood,
	)

	return prompt
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

func sendToBackend(url string, payload ResultPayload) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Ошибка маршалинга: %v", err)
		return
	}

	resp, err := http.Post(url+"/api/test-results", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Ошибка отправки на бэкенд: %v", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Ответ бэкенда: %s", resp.Status)
}

func send(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Ошибка отправки сообщения: %v", err)
	}
}
