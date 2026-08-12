package service

import "hotpot-iptv/internal/channel/app"

type Client struct {
	app app.Application
}

func NewClient(a app.Application) Client { return Client{app: a} }
