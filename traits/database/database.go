package database

import (
	"database/sql"
	"fmt"
	"log"
	"tanysu-bot/config"

	_ "github.com/mattn/go-sqlite3"
)

// DatabaseConnection устанавливает подключение к SQLite и автоматически создаёт необходимые таблицы.
func DatabaseConnection(cfg *config.Config) *sql.DB {
	// Путь к файлу базы данных из конфигурации
	dbPath := cfg.DBName

	// Строка подключения с поддержкой внешних ключей
	connStr := fmt.Sprintf("%s?_foreign_keys=on", dbPath)

	// Открываем соединение к базе
	db, err := sql.Open("sqlite3", connStr)
	if err != nil {
		log.Fatalf("Ошибка при открытии подключения к SQLite: %v", err)
	}

	// Проверяем соединение
	if err := db.Ping(); err != nil {
		log.Fatalf("Ошибка при подключении к SQLite: %v", err)
	}
	log.Println("Успешное подключение к SQLite!")

	// Создаём таблицу doktok, если она не существует
	createDoktok := `
CREATE TABLE IF NOT EXISTS doktok (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	id_user BIGINT,
	ava TEXT,
	fio TEXT,
	diploma TEXT,
	certificate TEXT,
	contact TEXT,
	time TEXT
);
`
	if _, err := db.Exec(createDoktok); err != nil {
		log.Fatalf("Ошибка при создании таблицы doktok: %v", err)
	}
	log.Println("Таблица doktok успешно создана (если не существовала).")

	// Создаём таблицу client, если она не существует
	createClient := `
CREATE TABLE IF NOT EXISTS client (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	id_user BIGINT,
	fio TEXT,
	sex TEXT,
	problem TEXT,
	period TEXT,
	med_personal TEXT,
	contact TEXT,
	address TEXT,
	time TEXT
);
`
	if _, err := db.Exec(createClient); err != nil {
		log.Fatalf("Ошибка при создании таблицы client: %v", err)
	}
	log.Println("Таблица client успешно создана (если не существовала).")

	// Создаём таблицу just, если она не существует
	createJust := `
CREATE TABLE IF NOT EXISTS just (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	id_user BIGINT,
	time TEXT
);
`
	if _, err := db.Exec(createJust); err != nil {
		log.Fatalf("Ошибка при создании таблицы just: %v", err)
	}
	log.Println("Таблица just успешно создана (если не существовала).")

	return db
}
