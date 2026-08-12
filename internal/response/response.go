package response

type HTTPResponse struct {
	Data  any    `json:"data"`
	Error string `json:"error,omitempty"`
}
