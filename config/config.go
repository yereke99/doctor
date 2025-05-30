package config

// Config содержит параметры конфигурации приложения.
type Config struct {
	Port                          string `json:"port"`
	Token                         string `json:"token"` // Токен для Telegram бота
	AvaDir                        string `json:"avaDir"`
	DocsDir                       string `json:"docsDir"`
	AdminID                       int64
	RedisAddr                     string `json:"redis_addr"`     // Адрес Redis
	RedisPassword                 string `json:"redis_password"` // Пароль для Redis
	RedisDB                       int    `json:"redis_db"`       // Номер базы данных Redis
	ChannelID                     int64  `json:"channelID"`      // Идентификатор канала
	ChannelName                   string `json:"channelName"`
	ExamplePhotoRegistrationId    string `json:"example_photo_reg_id"`
	ExamplePhotoRegistrationGeoId string `json:"example_photo_reg_geo_id"`

	// Параметры для SQLite (используем DBName как путь к файлу базы)
	DBName string `json:"db_name"`
}

// NewConfig создаёт и возвращает новый экземпляр конфигурации.
func NewConfig() (*Config, error) {
	cfg := &Config{
		Port:                          ":8080",
		Token:                         "8104980731:AAEN8KGxAwwfmPWa3s2yiPO7_EP-Cq2wbco",
		AvaDir:                        "./ava",
		DocsDir:                       "./documents",
		AdminID:                       800703982,
		RedisAddr:                     "localhost:6379",
		RedisPassword:                 "",
		RedisDB:                       0,
		ChannelID:                     2403228914,
		ChannelName:                   "@jaiAngmeAitamyz",
		DBName:                        "doctor.db", // Имя файла базы данных SQLite
		ExamplePhotoRegistrationId:    "AgACAgIAAxkBAAOkZ7ikJMnUHUnHpJIUBdIo54yMqjAAAlPvMRsXqclJ8FL8Er6DAAGZAQADAgADeQADNgQ",
		ExamplePhotoRegistrationGeoId: "AgACAgIAAxkBAAOnZ7ikRlKCnj2xEhc8YO8AARKCWXVgAAJU7zEbF6nJSRIgQCcxY8VPAQADAgADbQADNgQ",
	}
	return cfg, nil
}
