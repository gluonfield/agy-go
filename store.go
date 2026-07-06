package agy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Session struct {
	ID             string    `json:"id"`
	Cwd            string    `json:"cwd"`
	ConversationID string    `json:"conversation_id"`
	SaveDir        string    `json:"save_dir,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Store struct {
	Dir string
}

func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("store dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) Get(id string) (Session, bool, error) {
	all, err := s.load()
	if err != nil {
		return Session{}, false, err
	}
	session, ok := all[id]
	return session, ok, nil
}

func (s *Store) Put(session Session) error {
	all, err := s.load()
	if err != nil {
		return err
	}
	all[session.ID] = session
	return s.save(all)
}

func (s *Store) LastConversationForCwd(cwd string) (string, error) {
	path := filepath.Join(homeDir(), ".gemini", "antigravity-cli", "cache", "last_conversations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var byCwd map[string]string
	if err := json.Unmarshal(data, &byCwd); err != nil {
		return "", err
	}
	return byCwd[cwd], nil
}

func (s *Store) SessionDir(id string) (string, error) {
	if id == "" {
		return "", errors.New("session id is required")
	}
	path := filepath.Join(s.Dir, "sessions", id)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) load() (map[string]Session, error) {
	data, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return map[string]Session{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]Session
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]Session{}
	}
	return out, nil
}

func (s *Store) save(sessions map[string]Session) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".sessions-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path())
}

func (s *Store) path() string {
	return filepath.Join(s.Dir, "sessions.json")
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}
