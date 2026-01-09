package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AudioDir           string         `yaml:"audio_dir"`
	PullTimeout        string         `yaml:"pull_timeout"`
	MessageLimit       int            `yaml:"message_limit"`
	MotionResumeDelay  time.Duration  `yaml:"-"`
	PresenceClearDelay time.Duration  `yaml:"-"`
	UseWSSecurity      bool           `yaml:"use_ws_security"`
	PresenceTargets    []string       `yaml:"presence_targets"`
	Camera             CameraConfig   `yaml:"camera"`
	Router             RouterConfig   `yaml:"router"`
	Telegram           TelegramConfig `yaml:"telegram"`
}

type CameraConfig struct {
	IP       string `yaml:"ip"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type RouterConfig struct {
	BaseURL  string `yaml:"base_url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Lang     string `yaml:"lang"`
}

type TelegramConfig struct {
	Token  string `yaml:"token"`
	ChatID int64  `yaml:"chat_id"`
}

type rawConfig struct {
	AudioDir           string         `yaml:"audio_dir"`
	PullTimeout        string         `yaml:"pull_timeout"`
	MessageLimit       int            `yaml:"message_limit"`
	MotionResumeDelay  string         `yaml:"motion_resume_delay"`
	PresenceClearDelay string         `yaml:"presence_clear_delay"`
	UseWSSecurity      bool           `yaml:"use_ws_security"`
	PresenceTargets    []string       `yaml:"presence_targets"`
	Camera             CameraConfig   `yaml:"camera"`
	Router             RouterConfig   `yaml:"router"`
	Telegram           TelegramConfig `yaml:"telegram"`
}

var appConfig Config

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}

	delay := time.Duration(0)
	if raw.MotionResumeDelay != "" {
		delay, err = time.ParseDuration(raw.MotionResumeDelay)
		if err != nil {
			return Config{}, fmt.Errorf("invalid motion_resume_delay: %w", err)
		}
	}
	presenceDelay := time.Duration(0)
	if raw.PresenceClearDelay != "" {
		presenceDelay, err = time.ParseDuration(raw.PresenceClearDelay)
		if err != nil {
			return Config{}, fmt.Errorf("invalid presence_clear_delay: %w", err)
		}
	}

	return Config{
		AudioDir:           raw.AudioDir,
		PullTimeout:        raw.PullTimeout,
		MessageLimit:       raw.MessageLimit,
		MotionResumeDelay:  delay,
		PresenceClearDelay: presenceDelay,
		UseWSSecurity:      raw.UseWSSecurity,
		PresenceTargets:    raw.PresenceTargets,
		Camera:             raw.Camera,
		Router:             raw.Router,
		Telegram:           raw.Telegram,
	}, nil
}
