package storage

import (
	"context"
	"time"
)

type Settings struct {
	SessionDurationHours int  `json:"session_duration_hours"`
	RenewOnAccess        bool `json:"renew_on_access"`
}

func (s Settings) SessionDur() time.Duration {
	h := s.SessionDurationHours
	if h <= 0 {
		h = 168
	}
	return time.Duration(h) * time.Hour
}

var defaultSettings = Settings{SessionDurationHours: 168, RenewOnAccess: true}

func (s *Storage) GetSettings(ctx context.Context) (Settings, error) {
	var st Settings
	err := s.db.QueryRowContext(ctx,
		`SELECT session_duration_hours, renew_on_access FROM settings WHERE id = 1`,
	).Scan(&st.SessionDurationHours, &st.RenewOnAccess)
	if err != nil {
		return defaultSettings, nil
	}
	return st, nil
}

func (s *Storage) UpdateSettings(ctx context.Context, durationHours int, renewOnAccess bool) error {
	if durationHours <= 0 {
		durationHours = 168
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE settings SET session_duration_hours = ?, renew_on_access = ? WHERE id = 1`,
		durationHours, renewOnAccess)
	return err
}
