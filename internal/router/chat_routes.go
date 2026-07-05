package router

// hostFrom normalises the listen host so it's always a non-empty address for
// the loopback URL. An empty "all interfaces" config maps to 127.0.0.1 — we
// never want the chat host to call out on a public interface.
func hostFrom(h string) string {
	if h == "" || h == "0.0.0.0" || h == "::" {
		return "127.0.0.1"
	}
	return h
}
