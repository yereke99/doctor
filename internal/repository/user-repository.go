package repository

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// ClientRegistration отражает запись клиента в БД
// Поле Time можно заменить на int64 (Unix timestamp) для удобства
type ClientRegistration struct {
	ID          int64
	UserID      int64
	Fio         string
	Sex         string
	Problem     string
	Period      string
	MedPersonal string
	Contact     string
	Address     string
	Time        string
}

// UserRepository управляет таблицей client
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository открывает соединение и создаёт таблицу client, если её нет
func NewUserRepository(dbPath string) (*UserRepository, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	// создаём таблицу client
	create := `
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
);`
	if _, err = db.Exec(create); err != nil {
		return nil, fmt.Errorf("create client table: %w", err)
	}
	return &UserRepository{db: db}, nil
}

// Insert добавляет нового клиента в таблицу
func (r *UserRepository) Insert(c *ClientRegistration) error {
	query := `INSERT INTO client (id_user, fio, sex, problem, period, med_personal, contact, address, time)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.Exec(query,
		c.UserID,
		c.Fio,
		c.Sex,
		c.Problem,
		c.Period,
		c.MedPersonal,
		c.Contact,
		c.Address,
		c.Time,
	)
	if err != nil {
		return fmt.Errorf("insert client: %w", err)
	}
	return nil
}

// Exists проверяет, существует ли клиент с данным UserID
func (r *UserRepository) Exists(userID int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM client WHERE id_user = ?)`
	err := r.db.QueryRow(query, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("exists check: %w", err)
	}
	return exists, nil
}

// Update обновляет непустые поля клиента по UserID
func (r *UserRepository) Update(c *ClientRegistration) error {
	sets := []string{}
	args := []interface{}{}
	if c.Fio != "" {
		sets = append(sets, "fio = ?")
		args = append(args, c.Fio)
	}
	if c.Sex != "" {
		sets = append(sets, "sex = ?")
		args = append(args, c.Sex)
	}
	if c.Problem != "" {
		sets = append(sets, "problem = ?")
		args = append(args, c.Problem)
	}
	if c.Period != "" {
		sets = append(sets, "period = ?")
		args = append(args, c.Period)
	}
	if c.MedPersonal != "" {
		sets = append(sets, "med_personal = ?")
		args = append(args, c.MedPersonal)
	}
	if c.Contact != "" {
		sets = append(sets, "contact = ?")
		args = append(args, c.Contact)
	}
	if c.Address != "" {
		sets = append(sets, "address = ?")
		args = append(args, c.Address)
	}
	if c.Time != "" {
		sets = append(sets, "time = ?")
		args = append(args, c.Time)
	}
	if len(sets) == 0 {
		return nil // нечего обновлять
	}
	query := fmt.Sprintf("UPDATE client SET %s WHERE id_user = ?", strings.Join(sets, ", "))
	args = append(args, c.UserID)
	if _, err := r.db.Exec(query, args...); err != nil {
		return fmt.Errorf("update client: %w", err)
	}
	return nil
}
