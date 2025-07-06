package domain

type DoctorRegistration struct {
	ID          int64
	FullName    string
	Specialty   string
	Contact     string
	TelegramID  int64
	AvatarPath  string
	DiplomaPath string
	CertPath    string
}

type UserState struct {
	State         string `json:"state"`
	BroadCastType string `json:"broadcast_type"`
	Count         int    `json:"count"`
	Contact       string `json:"contact"`
	IsPaid        bool   `json:"is_paid"`
}
