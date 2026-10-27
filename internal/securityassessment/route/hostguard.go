package route

// HostGuard isolates one attempt's endpoint address from other host listeners.
// Its address and ownership must be journaled before Open. Guard operations
// require Linux; other platforms fail closed before assessment allocation.
type HostGuard struct {
	Owner   string `json:"owner"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Socket  string `json:"socket,omitempty"`
	created bool
}
