package config

import "time"

// Refresh configures the scan budget and reserved credential-maintenance capacity.
type Refresh struct {
	LeadTime     time.Duration `mapstructure:"lead_time"`
	ScanInterval time.Duration `mapstructure:"scan_interval"`
	Concurrency  int           `mapstructure:"concurrency"`
}

// MaintenanceLeadTime is applied once, when a credential has no platform refresh_at.
func (r Refresh) MaintenanceLeadTime() time.Duration {
	if r.LeadTime <= 0 {
		return 5 * time.Minute
	}
	return r.LeadTime
}

// Interval bounds scanning independently of notification delivery.
func (r Refresh) Interval() time.Duration {
	if r.ScanInterval <= 0 {
		return time.Minute
	}
	return r.ScanInterval
}

// ReservedConcurrency cannot be consumed by notification traffic.
func (r Refresh) ReservedConcurrency() int {
	if r.Concurrency <= 0 {
		return 1
	}
	return r.Concurrency
}
