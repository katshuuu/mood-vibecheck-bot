package types

// TestSession хранит состояние теста пользователя
type TestSession struct {
	Step   int
	Scores map[string]float64
}

// ResultPayload структура для отправки на бэкенд
type ResultPayload struct {
	TelegramID int64              `json:"telegram_id"`
	Profile    map[string]string  `json:"profile"`
	Scores     map[string]float64 `json:"scores"`
	AIPrompt   string             `json:"ai_prompt"`
}

// Question структура вопроса (для возможного расширения)
type Question struct {
	Step    int
	Text    string
	Options []Option
}

// Option вариант ответа
type Option struct {
	Code  string
	Text  string
	Image string // URL картинки (опционально)
}
