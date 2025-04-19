package repository

import (
	"database/sql"
	"fmt"
)

// DoctorRegistration отражает запись доктора в базе.
type DoctorRegistration struct {
	ID          int64
	FullName    string
	Contact     string
	TelegramID  int64
	AvatarPath  string
	DiplomaPath string
	CertPath    string
	Time        string
}

// DoctorRepository управляет операциями с таблицей doktok.
type DoctorRepository struct {
	db *sql.DB
}

// NewDoctorRepository создаёт новый репозиторий для работы с докторами.
func NewDoctorRepository(db *sql.DB) *DoctorRepository {
	return &DoctorRepository{db: db}
}

// Insert добавляет нового доктора в таблицу doktok.
func (r *DoctorRepository) Insert(doc *DoctorRegistration) error {
	query := `INSERT INTO doktok (
		id_user, ava, fio, diploma, certificate, contact, time
	) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.Exec(query,
		doc.TelegramID,
		doc.AvatarPath,
		doc.FullName,
		doc.DiplomaPath,
		doc.CertPath,
		doc.Contact,
		doc.Time,
	)
	if err != nil {
		return fmt.Errorf("не удалось вставить запись доктора: %w", err)
	}
	return nil
}

// Check проверяет, существует ли доктор с данным TelegramID.
func (r *DoctorRepository) Check(doctorId int64) (bool, error) {
	var exists bool
	query := `SELECT EXISTS (SELECT 1 FROM doktok WHERE id_user = ?)`
	if err := r.db.QueryRow(query, doctorId).Scan(&exists); err != nil {
		return false, fmt.Errorf("ошибка проверки существования доктора: %w", err)
	}
	return exists, nil
}

// DoctorUpdate обновляет поля доктора, которые не пусты, по его TelegramID.
func (r *DoctorRepository) DoctorUpdate(doc *DoctorRegistration) error {
	// Сбор динамических частей запроса
	setClauses := []string{}
	args := []interface{}{}

	if doc.AvatarPath != "" {
		setClauses = append(setClauses, "ava = ?")
		args = append(args, doc.AvatarPath)
	}
	if doc.FullName != "" {
		setClauses = append(setClauses, "fio = ?")
		args = append(args, doc.FullName)
	}
	if doc.DiplomaPath != "" {
		setClauses = append(setClauses, "diploma = ?")
		args = append(args, doc.DiplomaPath)
	}
	if doc.CertPath != "" {
		setClauses = append(setClauses, "certificate = ?")
		args = append(args, doc.CertPath)
	}
	if doc.Contact != "" {
		setClauses = append(setClauses, "contact = ?")
		args = append(args, doc.Contact)
	}

	if len(setClauses) == 0 {
		// Нет полей для обновления
		return nil
	}

	sql := fmt.Sprintf(
		"UPDATE doktok SET %s WHERE id_user = ?",
		string(join(setClauses, ", ")),
	)
	// Добавляем в конец аргумент для WHERE
	args = append(args, doc.TelegramID)

	_, err := r.db.Exec(sql, args...)
	if err != nil {
		return fmt.Errorf("не удалось обновить данные доктора: %w", err)
	}
	return nil
}

// вспомогательная функция для объединения строк
func join(items []string, sep string) string {
	result := ""
	for i, s := range items {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
