package telemetry

import "time"

// TokenStatus represents the lifecycle state of a pre-assigned token.
type TokenStatus string

const (
	TokenStatusUnbound TokenStatus = "UNBOUND" // Token created, not yet claimed by any client
	TokenStatusActive  TokenStatus = "ACTIVE"  // Token bound to an app and instance, receiving heartbeats
	TokenStatusRevoked TokenStatus = "REVOKED" // Token revoked, rejected by server
)

// InstanceStatus represents the health/liveness state of a reporting instance.
type InstanceStatus string

const (
	InstanceStatusOnline  InstanceStatus = "ONLINE"
	InstanceStatusOffline InstanceStatus = "OFFLINE"
)

// TokenRecord stores token metadata and binding in persistent storage.
type TokenRecord struct {
	Token        string      `json:"token"`
	AppName      string      `json:"app_name,omitempty"`      // Bound upon first heartbeat
	InstanceName string      `json:"instance_name,omitempty"`  // Bound upon first heartbeat
	Status       TokenStatus `json:"status"`
	CreatedAt    time.Time   `json:"created_at"`
	BoundAt      *time.Time  `json:"bound_at,omitempty"`
	LastSeenAt   *time.Time  `json:"last_seen_at,omitempty"`
}

// InstanceInfo represents the live runtime state of a registered service instance.
type InstanceInfo struct {
	Token        string         `json:"token"`
	AppName      string         `json:"app_name"`
	InstanceName string         `json:"instance_name"`
	Hostname     string         `json:"hostname"`
	OS           string         `json:"os"`
	Arch         string         `json:"arch"`
	Version      string         `json:"version"`
	Commit       string         `json:"commit,omitempty"`
	UptimeSec    int64          `json:"uptime_sec"`
	Goroutines   int            `json:"goroutines"`
	MemAllocMB   float64        `json:"mem_alloc_mb"`
	RemoteIP     string         `json:"remote_ip,omitempty"`
	Status       InstanceStatus `json:"status"`
	FirstSeenAt  time.Time      `json:"first_seen_at"`
	LastSeenAt   time.Time      `json:"last_seen_at"`
	CustomMeta   map[string]any `json:"custom_meta,omitempty"`
	LastTaskAck  *TaskAck       `json:"last_task_ack,omitempty"`
}

// HeartbeatPayload is sent by client reporters to the central Arks server.
type HeartbeatPayload struct {
	AppName      string         `json:"app_name"`
	InstanceName string         `json:"instance_name"` // Client-determined (from env or Hostname)
	Hostname     string         `json:"hostname"`
	OS           string         `json:"os"`
	Arch         string         `json:"arch"`
	Version      string         `json:"version"`
	Commit       string         `json:"commit,omitempty"`
	UptimeSec    int64          `json:"uptime_sec"`
	Goroutines   int            `json:"goroutines"`
	MemAllocMB   float64        `json:"mem_alloc_mb"`
	CustomMeta   map[string]any `json:"custom_meta,omitempty"`
	LastTaskAck  *TaskAck       `json:"last_task_ack,omitempty"`
}

// HeartbeatResponse is returned by Arks to the reporting client, optionally piggybacking a command.
type HeartbeatResponse struct {
	Ack        bool     `json:"ack"`
	ServerTime int64    `json:"server_time"`
	Message    string   `json:"message,omitempty"`
	Command    *Command `json:"command,omitempty"` // Non-nil when Arks has a pending task for this instance
}

// Command represents an action dispatched by Arks to a client.
type Command struct {
	ID        string       `json:"id"`
	Type      string       `json:"type"` // e.g. "UPGRADE", "RESTART", "PING"
	CreatedAt time.Time    `json:"created_at"`
	Upgrade   *UpgradeSpec `json:"upgrade,omitempty"`
}

// UpgradeSpec defines target binary download and checksum details for an UPGRADE command.
type UpgradeSpec struct {
	TargetVersion string `json:"target_version"`
	DownloadURL   string `json:"download_url"`
	SHA256        string `json:"sha256"`
	Force         bool   `json:"force"`
}

// TaskAck is reported back by the client after attempting to execute a command.
type TaskAck struct {
	CommandID  string    `json:"command_id"`
	Status     string    `json:"status"` // "SUCCESS" or "FAILED"
	ErrorMsg   string    `json:"error_msg,omitempty"`
	ExecutedAt time.Time `json:"executed_at"`
}
