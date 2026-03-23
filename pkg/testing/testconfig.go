package testing

import "time"

// TestConfig defines a single test configuration
type TestConfig struct {
	LocalIP          string        `json:"local_ip"`
	IPs              string        `json:"ips"`
	Protocol         string        `json:"protocol"`
	TotalConnections int           `json:"total_connections"`
	Concurrency      int           `json:"concurrency"`
	Duration         time.Duration `json:"duration"`
	ReportInterval   time.Duration `json:"report_interval"`
	PacketSize       int           `json:"packet_size"`
	Port             int           `json:"port"`
	PPS              int           `json:"pps"`
	ThroughputMode   bool          `json:"throughput_mode"`
	BufferSize       int           `json:"buffer_size"`
	TCPNoDelay       bool          `json:"tcp_no_delay"`
	UseOptimized     bool          `json:"use_optimized"`
	EnableIOUring    bool          `json:"enable_io_uring"`
	EnableHugePages  bool          `json:"enable_huge_pages"`
	EnableOffload    bool          `json:"enable_offload"`
	SendBatchSize    int           `json:"send_batch_size"`
	RecvBatchSize    int           `json:"recv_batch_size"`
	NumQueues        int           `json:"num_queues"`
	BusyPollUsecs    int           `json:"busy_poll_usecs"`
	TCPCork          bool          `json:"tcp_cork"`
	TCPQuickAck      bool          `json:"tcp_quick_ack"`
	MemArenaSize     int           `json:"mem_arena_size"`
	RingSize         int           `json:"ring_size"`
	NumWorkers       int           `json:"num_workers"`
	CPUList          string        `json:"cpu_list"`
	ReportTo         string        `json:"report_to"`
	ApiServerPort    int           `json:"api_server_port"`
}

// GetDefaultTestConfig returns a TestConfig with sensible defaults for all fields
func GetDefaultTestConfig() *TestConfig {
	return &TestConfig{
		Protocol:         "tcp",
		TotalConnections: 64,
		Concurrency:      0, // Will be auto-calculated
		Duration:         5 * time.Minute,
		ReportInterval:   5 * time.Second,
		PacketSize:       0, // Will be auto-detected
		Port:             9999,
		PPS:              0, // Unlimited
		ThroughputMode:   true,
		BufferSize:       0, // Will be auto-calculated
		TCPNoDelay:       false,
		UseOptimized:     true,
		EnableIOUring:    true,
		EnableHugePages:  true,
		EnableOffload:    true,
		SendBatchSize:    64,
		RecvBatchSize:    64,
		NumQueues:        0, // Will be auto-calculated
		BusyPollUsecs:    50,
		TCPCork:          false,
		TCPQuickAck:      false,
		MemArenaSize:     268435456, // 256MB
		RingSize:         4096,
		NumWorkers:       0, // Will be auto-calculated
		CPUList:          "",
		ReportTo:         "",
		ApiServerPort:    0,
	}
}

// MergeTestConfig applies non-zero values from provided config over defaults
// Used to allow partial config updates while preserving defaults for unspecified fields
func MergeTestConfig(defaults, provided *TestConfig) *TestConfig {
	if provided == nil {
		return defaults
	}

	merged := &TestConfig{
		LocalIP:       provided.LocalIP,
		IPs:           provided.IPs,
		ReportTo:      provided.ReportTo,
		CPUList:       provided.CPUList,
		ApiServerPort: provided.ApiServerPort,
	}

	// Copy over provided values, or use defaults if not specified (zero values)
	if provided.Protocol != "" {
		merged.Protocol = provided.Protocol
	} else {
		merged.Protocol = defaults.Protocol
	}

	if provided.TotalConnections != 0 {
		merged.TotalConnections = provided.TotalConnections
	} else {
		merged.TotalConnections = defaults.TotalConnections
	}

	if provided.Concurrency != 0 {
		merged.Concurrency = provided.Concurrency
	} else {
		merged.Concurrency = defaults.Concurrency
	}

	if provided.Duration != 0 {
		merged.Duration = provided.Duration
	} else {
		merged.Duration = defaults.Duration
	}

	if provided.ReportInterval != 0 {
		merged.ReportInterval = provided.ReportInterval
	} else {
		merged.ReportInterval = defaults.ReportInterval
	}

	if provided.PacketSize != 0 {
		merged.PacketSize = provided.PacketSize
	} else {
		merged.PacketSize = defaults.PacketSize
	}

	if provided.Port != 0 {
		merged.Port = provided.Port
	} else {
		merged.Port = defaults.Port
	}

	if provided.PPS != 0 {
		merged.PPS = provided.PPS
	} else {
		merged.PPS = defaults.PPS
	}

	merged.ThroughputMode = provided.ThroughputMode // Always use provided (explicit boolean)

	if provided.BufferSize != 0 {
		merged.BufferSize = provided.BufferSize
	} else {
		merged.BufferSize = defaults.BufferSize
	}

	merged.TCPNoDelay = provided.TCPNoDelay
	merged.UseOptimized = provided.UseOptimized
	merged.EnableIOUring = provided.EnableIOUring
	merged.EnableHugePages = provided.EnableHugePages
	merged.EnableOffload = provided.EnableOffload

	if provided.SendBatchSize != 0 {
		merged.SendBatchSize = provided.SendBatchSize
	} else {
		merged.SendBatchSize = defaults.SendBatchSize
	}

	if provided.RecvBatchSize != 0 {
		merged.RecvBatchSize = provided.RecvBatchSize
	} else {
		merged.RecvBatchSize = defaults.RecvBatchSize
	}

	if provided.NumQueues != 0 {
		merged.NumQueues = provided.NumQueues
	} else {
		merged.NumQueues = defaults.NumQueues
	}

	if provided.BusyPollUsecs != 0 {
		merged.BusyPollUsecs = provided.BusyPollUsecs
	} else {
		merged.BusyPollUsecs = defaults.BusyPollUsecs
	}

	merged.TCPCork = provided.TCPCork
	merged.TCPQuickAck = provided.TCPQuickAck

	if provided.MemArenaSize != 0 {
		merged.MemArenaSize = provided.MemArenaSize
	} else {
		merged.MemArenaSize = defaults.MemArenaSize
	}

	if provided.RingSize != 0 {
		merged.RingSize = provided.RingSize
	} else {
		merged.RingSize = defaults.RingSize
	}

	if provided.NumWorkers != 0 {
		merged.NumWorkers = provided.NumWorkers
	} else {
		merged.NumWorkers = defaults.NumWorkers
	}

	return merged
}
