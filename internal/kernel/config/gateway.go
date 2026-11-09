package config

// Gateway defines shared Redis token buckets and bounded public read execution.
type Gateway struct {
	GlobalQPS   int `mapstructure:"global_qps"`
	GlobalBurst int `mapstructure:"global_burst"`
	ClientQPS   int `mapstructure:"client_qps"`
	ClientBurst int `mapstructure:"client_burst"`
	RouteQPS    int `mapstructure:"route_qps"`
	RouteBurst  int `mapstructure:"route_burst"`
}

// DefaultGateway supplies explicit policies without deriving QPS from call quotas.
func DefaultGateway() Gateway {
	return Gateway{GlobalQPS: 5000, GlobalBurst: 10000, ClientQPS: 10, ClientBurst: 20, RouteQPS: 1000, RouteBurst: 2000}
}
