package main

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"github.com/lxbot/script-docker/buff"
	"github.com/mohemohe/temple"
	"log"
	"os"
	"os/exec"
	"plugin"
	"strings"
	"sync"
	"time"
)

type M = map[string]interface{}

const lines = 30

var store *plugin.Plugin
var ch *chan M

var images = map[string]string{
	"bash":   "archlinux/base",
	"node":   "node:lts-alpine",
	"php":    "php:alpine",
	"python": "python:alpine",
	"ruby":   "ruby:alpine",
}

func Boot(s *plugin.Plugin, c *chan M) {
	store = s
	ch = c

	gob.Register(M{})
	gob.Register([]interface{}{})
}

func Help() string {
	t := `{{.p}}bash: run command in "archlinux/base:latest"
{{.p}}node: run script in "node:lts-alpine"
{{.p}}php: run script in "php:alpine"
{{.p}}python: run script in "python:alpine"
{{.p}}ruby: run script in "ruby:alpine"
`
	m := M{
		"p": os.Getenv("LXBOT_COMMAND_PREFIX"),
	}
	r, _ := temple.Execute(t, m)
	return r
}

func OnMessage() []func(M) M {
	return []func(M) M{
		func(msg M) M {
			text := msg["message"].(M)["text"].(string)
			img, ok := shouldHandle(text)
			if !ok {
				return nil
			}

			log.Println("[docker]", "use:", img)
			script := extractScript(text)
			log.Println("[docker]", "script:", script)
			run(msg, img, script)
			return nil
		},
	}
}

func shouldHandle(text string) (string, bool) {
	result := false
	img := ""
	p := os.Getenv("LXBOT_COMMAND_PREFIX")
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

func deepCopy(msg M) (M, error) {
	var b bytes.Buffer
	e := gob.NewEncoder(&b)
	d := gob.NewDecoder(&b)
	if err := e.Encode(msg); err != nil {
		return nil, err
	}
	r := map[string]interface{}{}
	if err := d.Decode(&r); err != nil {
		return nil, err
	}
	return r, nil
}

func generateText(stdout []string, stderr []string, tag string) string {
	stdoutText := strings.Join(stdout, "\n")
	stderrText := strings.Join(stderr, "\n")

	return "STDOUT " + tag + "\n\n" + "```\n" + stdoutText + "\n```\n\nSTDERR " + tag + "\n\n" + "```\n" + stderrText + "\n```"
}

func run(msg M, img string, script string) {
	args := []string{"run", "--rm", "-i", "--log-driver", "none"}
	network := os.Getenv("LXBOT_ALLOW_DOCKER_NETWORK")
	if network != "true" {
		args = append(args, "--network", "none")
	}
	args = append(args, img)

	cmd := exec.Command("docker", args...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Start()

	stdoutBuff := buff.NewBuff()
	stderrBuff := buff.NewBuff()
	buffCh := make(chan int)
	timeout := false

	wg := &sync.WaitGroup{}
	go func() {
		wg.Add(1)

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			stdoutBuff.Enqueue(scanner.Text())
			buffCh <- 1
		}

		wg.Done()
	}()
	go func() {
		wg.Add(1)

		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			stderrBuff.Enqueue(scanner.Text())
			buffCh <- 1
		}

		wg.Done()
	}()
	go func() {
		waitCh := time.After(3 * time.Second)

		for {
			select {
			case _, ok := <-buffCh:
				if !ok {
					return
				}
				if stdoutBuff.Len() >= lines || stderrBuff.Len() >= lines {
					waitCh = time.After(3 * time.Second)

					stdoutLines := stdoutBuff.BulkDequeue(lines)
					stderrLines := stderrBuff.BulkDequeue(lines)
					text := generateText(stdoutLines, stderrLines, "(PARTIAL)")

					// FIXME: copy error
					nextMsg, _ := deepCopy(msg)
					nextMsg["mode"] = "reply"
					nextMsg["message"].(M)["text"] = text
					*ch <- nextMsg
				}
				break
			case <-waitCh:
				waitCh = time.After(3 * time.Second)

				stdoutLines := stdoutBuff.DequeueALL()
				stderrLines := stderrBuff.DequeueALL()
				text := generateText(stdoutLines, stderrLines, "(PARTIAL)")

				// FIXME: copy error
				nextMsg, _ := deepCopy(msg)
				nextMsg["mode"] = "reply"
				nextMsg["message"].(M)["text"] = text
				*ch <- nextMsg
				break
			case <-time.After(3 * time.Minute):
				if !cmd.ProcessState.Exited() {
					_ = cmd.Process.Kill()
				}
				timeout = true
				return
			}
		}
	}()

	_, _ = stdin.Write([]byte(script))
	_ = stdin.Close()

	wg.Wait()
	cmd.Wait()
	close(buffCh)

	tag := "(FINISH)"
	if timeout {
		tag = "(TIMEOUT)"
	}
	stdoutLines := stdoutBuff.DequeueALL()
	stderrLines := stderrBuff.DequeueALL()
	text := generateText(stdoutLines, stderrLines, tag)

	// FIXME: copy error
	nextMsg, err := deepCopy(msg)
	if err != nil {
		log.Println(err)
		return
	}
	nextMsg["mode"] = "reply"
	nextMsg["message"].(M)["text"] = text
	*ch <- nextMsg
}
