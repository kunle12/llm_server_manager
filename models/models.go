package models

import "time"

type ModelConfig struct {
	Name        string   `json:"name" mapstructure:"name"`
	ModelPath   string   `json:"model_path" mapstructure:"model_path"`
	ContextSize int      `json:"context_size" mapstructure:"context_size"`
	Temperature float64  `json:"temperature" mapstructure:"temperature"`
	TopK        *int     `json:"top_k,omitempty" mapstructure:"top_k,omitempty"`
	TopP        *float64 `json:"top_p,omitempty" mapstructure:"top_p,omitempty"`
	Threads     int      `json:"threads" mapstructure:"threads"`
	Port        *int     `json:"port,omitempty" mapstructure:"port,omitempty"`
	Mmproj      *string  `json:"mmproj,omitempty" mapstructure:"mmproj,omitempty"`
}

type ServerStatus string

const (
	StatusStopped  ServerStatus = "stopped"
	StatusStarting ServerStatus = "starting"
	StatusRunning  ServerStatus = "running"
	StatusStopping ServerStatus = "stopping"
)

type RunningServer struct {
	ModelConfig ModelConfig  `json:"model_config"`
	PID         int          `json:"pid"`
	Status      ServerStatus `json:"status"`
	StartTime   time.Time    `json:"start_time"`
}

type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type ModelListResponse struct {
	Models []ModelItem `json:"models"`
}

type ModelItem struct {
	ModelConfig
	Active bool `json:"active"`
}

type ServerInfoResponse struct {
	Server *RunningServer `json:"server"`
}
