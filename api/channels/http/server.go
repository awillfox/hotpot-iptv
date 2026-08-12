package http

import "hotpot-iptv/api/channels/service"

type Server struct {
	svc service.Client
}

func NewServer(svc service.Client) Server { return Server{svc: svc} }
