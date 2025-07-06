package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ClientRegistration отражает запись клиента в БД
type ClientRegistration struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"user_id"`
	Fio         string `json:"fio"`
	Sex         string `json:"sex"`
	Problem     string `json:"problem"`
	Period      string `json:"period"`
	MedPersonal string `json:"med_personal"`
	Contact     string `json:"contact"`
	Address     string `json:"address"`
	Time        string `json:"time"`
}

// UserAgreement отражает согласие пользователя с условиями
type UserAgreement struct {
	ID                       int64     `json:"id"`
	TelegramID               int64     `json:"telegram_id"`
	UserType                 string    `json:"user_type"` // "doctor" or "patient"
	DoctorAgreementAccepted  bool      `json:"doctor_agreement_accepted"`
	PatientAgreementAccepted bool      `json:"patient_agreement_accepted"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// UserStatus представляет объединенный статус пользователя
type UserStatus struct {
	TelegramID               int64 `json:"telegram_id"`
	IsDoctor                 bool  `json:"is_doctor"`
	IsClient                 bool  `json:"is_client"`
	DoctorAgreementAccepted  bool  `json:"doctor_agreement_accepted"`
	PatientAgreementAccepted bool  `json:"patient_agreement_accepted"`
}

// UserRepository управляет пользовательскими данными
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository создает новый репозиторий и инициализирует таблицы
func NewUserRepository(dbPath string) (*UserRepository, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	repo := &UserRepository{db: db}
	if err = repo.createTables(); err != nil {
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return repo, nil
}

// createTables создает необходимые таблицы
func (r *UserRepository) createTables() error {
	// Таблица для клиентов
	clientTable := `
CREATE TABLE IF NOT EXISTS client (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	id_user BIGINT UNIQUE NOT NULL,
	fio TEXT,
	sex TEXT,
	problem TEXT,
	period TEXT,
	med_personal TEXT,
	contact TEXT,
	address TEXT,
	time TEXT,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`

	// Таблица для пользовательских соглашений
	agreementTable := `
CREATE TABLE IF NOT EXISTS user_agreements (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	telegram_id BIGINT NOT NULL,
	user_type TEXT NOT NULL,
	doctor_agreement_accepted BOOLEAN DEFAULT FALSE,
	patient_agreement_accepted BOOLEAN DEFAULT FALSE,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(telegram_id, user_type)
);`

	// Создаем индексы для быстрого поиска
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_client_user_id ON client(id_user);`,
		`CREATE INDEX IF NOT EXISTS idx_agreements_telegram_id ON user_agreements(telegram_id);`,
		`CREATE INDEX IF NOT EXISTS idx_agreements_user_type ON user_agreements(user_type);`,
	}

	// Выполняем создание таблиц
	if _, err := r.db.Exec(clientTable); err != nil {
		return fmt.Errorf("create client table: %w", err)
	}

	if _, err := r.db.Exec(agreementTable); err != nil {
		return fmt.Errorf("create user_agreements table: %w", err)
	}

	// Создаем индексы
	for _, index := range indexes {
		if _, err := r.db.Exec(index); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	return nil
}

// === CLIENT OPERATIONS ===

// InsertClient добавляет нового клиента в таблицу
func (r *UserRepository) InsertClient(c *ClientRegistration) error {
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

// ClientExists проверяет, существует ли клиент с данным UserID
func (r *UserRepository) ClientExists(userID int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM client WHERE id_user = ?)`
	err := r.db.QueryRow(query, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("client exists check: %w", err)
	}
	return exists, nil
}

// UpdateClient обновляет непустые поля клиента по UserID
func (r *UserRepository) UpdateClient(c *ClientRegistration) error {
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

	// Добавляем обновление времени изменения
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")

	query := fmt.Sprintf("UPDATE client SET %s WHERE id_user = ?", strings.Join(sets, ", "))
	args = append(args, c.UserID)

	if _, err := r.db.Exec(query, args...); err != nil {
		return fmt.Errorf("update client: %w", err)
	}
	return nil
}

// GetClientByUserID возвращает клиента по UserID
func (r *UserRepository) GetClientByUserID(userID int64) (*ClientRegistration, error) {
	query := `SELECT id, id_user, fio, sex, problem, period, med_personal, contact, address, time 
			  FROM client WHERE id_user = ?`

	client := &ClientRegistration{}
	err := r.db.QueryRow(query, userID).Scan(
		&client.ID,
		&client.UserID,
		&client.Fio,
		&client.Sex,
		&client.Problem,
		&client.Period,
		&client.MedPersonal,
		&client.Contact,
		&client.Address,
		&client.Time,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("client not found")
		}
		return nil, fmt.Errorf("get client: %w", err)
	}

	return client, nil
}

// === USER AGREEMENT OPERATIONS ===

// GetAllJustUserIDs returns all user IDs from just table
func (r *UserRepository) GetAllJustUserIDs(ctx context.Context) ([]int64, error) {
	const q = `SELECT id_user FROM just ORDER BY created_at DESC;`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs, nil
}

// SaveUserAgreement сохраняет согласие пользователя
func (r *UserRepository) SaveUserAgreement(telegramID int64, userType string, agreed bool) error {
	// Проверяем, существует ли уже запись
	var existingID int64
	checkQuery := `SELECT id FROM user_agreements WHERE telegram_id = ? AND user_type = ?`
	err := r.db.QueryRow(checkQuery, telegramID, userType).Scan(&existingID)

	if err == sql.ErrNoRows {
		// Создаем новую запись
		insertQuery := `INSERT INTO user_agreements (telegram_id, user_type, doctor_agreement_accepted, patient_agreement_accepted) 
						VALUES (?, ?, ?, ?)`

		var doctorAgreed, patientAgreed bool
		if userType == "doctor" {
			doctorAgreed = agreed
		} else {
			patientAgreed = agreed
		}

		_, err = r.db.Exec(insertQuery, telegramID, userType, doctorAgreed, patientAgreed)
		if err != nil {
			return fmt.Errorf("insert user agreement: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("check existing agreement: %w", err)
	} else {
		// Обновляем существующую запись
		var updateQuery string
		if userType == "doctor" {
			updateQuery = `UPDATE user_agreements SET doctor_agreement_accepted = ?, updated_at = CURRENT_TIMESTAMP 
						   WHERE telegram_id = ? AND user_type = ?`
		} else {
			updateQuery = `UPDATE user_agreements SET patient_agreement_accepted = ?, updated_at = CURRENT_TIMESTAMP 
						   WHERE telegram_id = ? AND user_type = ?`
		}

		_, err = r.db.Exec(updateQuery, agreed, telegramID, userType)
		if err != nil {
			return fmt.Errorf("update user agreement: %w", err)
		}
	}

	return nil
}

// GetUserAgreement получает согласие пользователя по типу
func (r *UserRepository) GetUserAgreement(telegramID int64, userType string) (*UserAgreement, error) {
	query := `SELECT id, telegram_id, user_type, doctor_agreement_accepted, patient_agreement_accepted, 
			  created_at, updated_at FROM user_agreements WHERE telegram_id = ? AND user_type = ?`

	agreement := &UserAgreement{}
	err := r.db.QueryRow(query, telegramID, userType).Scan(
		&agreement.ID,
		&agreement.TelegramID,
		&agreement.UserType,
		&agreement.DoctorAgreementAccepted,
		&agreement.PatientAgreementAccepted,
		&agreement.CreatedAt,
		&agreement.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // не найдено
		}
		return nil, fmt.Errorf("get user agreement: %w", err)
	}

	return agreement, nil
}

// GetUserStatus возвращает комплексный статус пользователя
func (r *UserRepository) GetUserStatus(telegramID int64, doctorRepo DoctorChecker) (*UserStatus, error) {
	status := &UserStatus{
		TelegramID:               telegramID,
		IsDoctor:                 false,
		IsClient:                 false,
		DoctorAgreementAccepted:  false,
		PatientAgreementAccepted: false,
	}

	// Проверяем, является ли пользователь доктором
	if doctorRepo != nil {
		isDoctor, err := doctorRepo.CheckDoctor(telegramID)
		if err == nil && isDoctor {
			status.IsDoctor = true
		}
		// Log for debugging
		fmt.Printf("Doctor check for %d: isDoctor=%v, err=%v\n", telegramID, isDoctor, err)
	}

	// Проверяем, является ли пользователь клиентом
	isClient, err := r.ClientExists(telegramID)
	if err == nil {
		status.IsClient = isClient
	}

	// Получаем информацию о согласиях
	doctorAgreement, err := r.GetUserAgreement(telegramID, "doctor")
	if err == nil && doctorAgreement != nil {
		status.DoctorAgreementAccepted = doctorAgreement.DoctorAgreementAccepted
	}

	patientAgreement, err := r.GetUserAgreement(telegramID, "patient")
	if err == nil && patientAgreement != nil {
		status.PatientAgreementAccepted = patientAgreement.PatientAgreementAccepted
	}

	// Log the final status for debugging
	fmt.Printf("Final user status for %d: %+v\n", telegramID, status)

	return status, nil
}

// === UTILITY METHODS ===

// Close закрывает соединение с базой данных
func (r *UserRepository) Close() error {
	return r.db.Close()
}

// DoctorChecker интерфейс для проверки статуса доктора
type DoctorChecker interface {
	CheckDoctor(telegramID int64) (bool, error)
}

// === LEGACY COMPATIBILITY ===

// Insert для обратной совместимости
func (r *UserRepository) Insert(c *ClientRegistration) error {
	return r.InsertClient(c)
}

// Exists для обратной совместимости
func (r *UserRepository) Exists(userID int64) (bool, error) {
	return r.ClientExists(userID)
}

// Update для обратной совместимости
func (r *UserRepository) Update(c *ClientRegistration) error {
	return r.UpdateClient(c)
}
