FROM golang:1.24.5-alpine

WORKDIR /app

# Копируем файлы зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Компилируем
RUN go build -o flower-bot .

# Запускаем (НЕ python, а скомпилированный бинарник!)
CMD ["./flower-bot"]

EXPOSE 8080