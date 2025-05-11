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
