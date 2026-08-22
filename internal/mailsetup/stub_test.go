package mailsetup

import "github.com/sislelabs/mailctl/internal"

// stubStore is a minimal in-package Store so sync tests need no filesystem.
type stubStore struct {
	cfg *internal.Config
}

func (s *stubStore) Load() (*internal.Config, error) {
	if s.cfg == nil {
		s.cfg = &internal.Config{DefaultForwardTo: "you@example.com"}
	}
	return s.cfg, nil
}

func (s *stubStore) Save(cfg *internal.Config) error {
	s.cfg = cfg
	return nil
}
