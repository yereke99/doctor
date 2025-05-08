package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DoctorRegistration отражает запись доктора в базе.
// Поля-указатели позволяют отличать "не задано" от пустой строки.
type DoctorRegistration struct {
	ID               int64      // внутренний PK
	TelegramID       int64      // соответствует колонке id_user
	FullName         *string    // fio
	TypeOfSpecialist *string    // тип специалиста (лор, кардиолог и т.п.)
	Contact          *string    // контакт (телефон, email)
	AvatarPath       *string    // путь к аватару
	DiplomaPath      *string    // путь к диплому
	CertPath         *string    // путь к сертификату
	Time             *time.Time // время создания/обновления записи
}

// DoctorRepository управляет операциями с таблицей doctor.
type DoctorRepository struct {
	db *sql.DB
}

// NewDoctorRepository создаёт новый репозиторий для работы с докторами.
func NewDoctorRepository(db *sql.DB) *DoctorRepository {
	return &DoctorRepository{db: db}
}

// Insert добавляет нового доктора в таблицу doctor.
// Предполагает, что все нужные поля (TelegramID, FullName и т.п.) заданы.
func (r *DoctorRepository) Insert(doc *DoctorRegistration) error {
	query := `
        INSERT INTO doctor (
            id_user,
            fio,
            type_specialist,
            contact,
            ava,
            diploma,
            certificate,
            time
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `
	_, err := r.db.Exec(
		query,
		doc.TelegramID,
		*doc.FullName,
		*doc.TypeOfSpecialist,
		*doc.Contact,
		*doc.AvatarPath,
		*doc.DiplomaPath,
		*doc.CertPath,
		*doc.Time,
	)
	if err != nil {
		return fmt.Errorf("не удалось вставить запись доктора: %w", err)
	}
	return nil
}

// CheckDoctor возвращает true, если в таблице уже есть доктор с данным telegram_id.
func (r *DoctorRepository) CheckDoctor(userId int64) (bool, error) {
	var foundID int64
	query := `SELECT id_user FROM doctor WHERE id_user = ? LIMIT 1`
	err := r.db.QueryRow(query, userId).Scan(&foundID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("ошибка проверки доктора: %w", err)
	}
	return true, nil
}

// UpdateDoctor обновляет непустые (ненулевые) поля доктора по telegram_id.
func (r *DoctorRepository) UpdateDoctor(doc *DoctorRegistration) error {
	setClauses := []string{}
	args := []interface{}{}

	if doc.FullName != nil {
		setClauses = append(setClauses, "fio = ?")
		args = append(args, *doc.FullName)
	}
	if doc.TypeOfSpecialist != nil {
		setClauses = append(setClauses, "type_specialist = ?")
		args = append(args, *doc.TypeOfSpecialist)
	}
	if doc.Contact != nil {
		setClauses = append(setClauses, "contact = ?")
		args = append(args, *doc.Contact)
	}
	if doc.AvatarPath != nil {
		setClauses = append(setClauses, "ava = ?")
		args = append(args, *doc.AvatarPath)
	}
	if doc.DiplomaPath != nil {
		setClauses = append(setClauses, "diploma = ?")
		args = append(args, *doc.DiplomaPath)
	}
	if doc.CertPath != nil {
		setClauses = append(setClauses, "certificate = ?")
		args = append(args, *doc.CertPath)
	}
	if doc.Time != nil {
		setClauses = append(setClauses, "time = ?")
		args = append(args, *doc.Time)
	}

	if len(setClauses) == 0 {
		// нечего обновлять
		return nil
	}

	// Собираем финальный запрос
	query := fmt.Sprintf(
		"UPDATE doctor SET %s WHERE id_user = ?",
		strings.Join(setClauses, ", "),
	)
	args = append(args, doc.TelegramID)

	_, err := r.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("не удалось обновить данные доктора: %w", err)
	}
	return nil
}
