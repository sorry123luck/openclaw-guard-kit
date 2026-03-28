package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ChannelCredentials stores credentials for all notification channels
type ChannelCredentials struct {
	Version  int                  `json:"version"`
	Telegram *TelegramCredentials `json:"telegram,omitempty"`
	Feishu   *FeishuCredentials   `json:"feishu,omitempty"`
	Wecom    *WecomCredentials    `json:"wecom,omitempty"`
}

type TelegramCredentials struct {
	BotToken string `json:"botToken"`
}

type FeishuCredentials struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
}

type WecomCredentials struct {
	BotID  string `json:"botId"`
	Secret string `json:"secret"`
}

// CredentialsStore manages channel credentials
type CredentialsStore struct {
	mu          sync.Mutex
	path        string
	credentials ChannelCredentials
}

var (
	credentialsStore     *CredentialsStore
	credentialsStoreOnce sync.Once
	credentialsRootDir   string
)

// SetCredentialsRootDir sets the root directory for credentials storage
func SetCredentialsRootDir(dir string) {
	credentialsRootDir = dir
}

// GetCredentialsRootDir returns the current root directory for credentials
func GetCredentialsRootDir() string {
	return credentialsRootDir
}

// NewCredentialsStore creates a new credentials store
func NewCredentialsStore(path string) (*CredentialsStore, error) {
	s := &CredentialsStore{
		path: path,
		credentials: ChannelCredentials{
			Version: 1,
		},
	}

	if err := s.Load(); err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	return s, nil
}

// Load loads credentials from file
func (s *CredentialsStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var creds ChannelCredentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return err
	}

	if creds.Version <= 0 {
		creds.Version = 1
	}

	s.credentials = creds
	return nil
}

// Save saves credentials to file
func (s *CredentialsStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *CredentialsStore) saveLocked() error {
	if s.credentials.Version <= 0 {
		s.credentials.Version = 1
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(s.credentials, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, raw, 0o644)
}

// GetTelegramCredentials returns Telegram credentials
func (s *CredentialsStore) GetTelegramCredentials() *TelegramCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credentials.Telegram
}

// SetTelegramCredentials sets Telegram credentials
func (s *CredentialsStore) SetTelegramCredentials(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.credentials.Telegram = &TelegramCredentials{
		BotToken: token,
	}

	return s.saveLocked()
}

// GetFeishuCredentials returns Feishu credentials
func (s *CredentialsStore) GetFeishuCredentials() *FeishuCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credentials.Feishu
}

// SetFeishuCredentials sets Feishu credentials
func (s *CredentialsStore) SetFeishuCredentials(appID, appSecret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.credentials.Feishu = &FeishuCredentials{
		AppID:     appID,
		AppSecret: appSecret,
	}

	return s.saveLocked()
}

// GetWecomCredentials returns Wecom credentials
func (s *CredentialsStore) GetWecomCredentials() *WecomCredentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credentials.Wecom
}

// SetWecomCredentials sets Wecom credentials
func (s *CredentialsStore) SetWecomCredentials(botID, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.credentials.Wecom = &WecomCredentials{
		BotID:  botID,
		Secret: secret,
	}

	return s.saveLocked()
}

// GetCredentialsStore returns the global credentials store instance
func GetCredentialsStore() *CredentialsStore {
	return credentialsStore
}

// InitCredentialsStore initializes the global credentials store
func InitCredentialsStore(root string) {
	credentialsRootDir = root

	path := CredentialsPath(root)
	if credentialsStore == nil {
		s, err := NewCredentialsStore(path)
		if err != nil {
			return
		}
		credentialsStore = s
		return
	}

	if credentialsStore.path != path {
		s, err := NewCredentialsStore(path)
		if err != nil {
			return
		}
		credentialsStore = s
		return
	}

	_ = credentialsStore.Load()
}

// GetTelegramToken returns Telegram bot token from credentials store
func GetTelegramToken() string {
	if credentialsStore == nil {
		return ""
	}
	cred := credentialsStore.GetTelegramCredentials()
	if cred == nil {
		return ""
	}
	return cred.BotToken
}

// GetFeishuCredentials returns Feishu app credentials from store
func GetFeishuCredentials() (appID, appSecret string) {
	if credentialsStore == nil {
		return "", ""
	}
	cred := credentialsStore.GetFeishuCredentials()
	if cred == nil {
		return "", ""
	}
	return cred.AppID, cred.AppSecret
}

// GetWecomCredentials returns Wecom credentials from store
func GetWecomCredentials() (botID, secret string) {
	if credentialsStore == nil {
		return "", ""
	}
	cred := credentialsStore.GetWecomCredentials()
	if cred == nil {
		return "", ""
	}
	return cred.BotID, cred.Secret
}
