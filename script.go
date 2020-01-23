package main

import (
	"os"
	"plugin"
	"strings"
)

type M = map[string]interface{}

var store *plugin.Plugin
var ch *chan M

var images = map[string]string{
	"node": "node:lts-alpine",
	"ruby": "ruby:alpine",
}

func Boot(s *plugin.Plugin, c *chan M) {
	store = s
	ch = c
}

func OnMessage() []func(M) M {
	return []func(M) M{
		func(msg M) M {
			text := msg["message"].(M)["text"].(string)
			img, ok := shouldHandle(text)
			if !ok {
				return nil
			}

			script := extractScript(text)
			exec(msg, img, script)

			msg["mode"] = "reply"
			return msg
		},
	}
}

func prefix() string {
	p := os.Getenv("LXBOT_COMMAND_PREFIX")
	if p == "" {
		p = "/"
	}
	return p
}

func shouldHandle(text string) (string, bool) {
	result := false
	img := ""
	p := prefix()
	for k, v := range images {
		s := p + k
		if strings.HasPrefix(text, s) {
			result = true
			img = v
		}
	}
	return img, result
}

func extractScript(text string) string {
	return strings.Join(strings.Split(text, "\n")[1:], "\n")
}

func exec(msg M, img string, script string) {

}