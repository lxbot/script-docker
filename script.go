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
	"strconv"
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
	log.Println("text:", text)
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
	result := ""
	if len(stdout) > 0 {
		result += "STDOUT " + tag + "\n\n" + "```\n" + strings.Join(stdout, "\n") + "\n```"
	}
	if len(stderr) > 0 {
		if result != "" {
			result += "\n\n"
		}
		result += "STDERR " + tag + "\n\n" + "```\n" + strings.Join(stderr, "\n") + "\n```"
	}
	result = strings.ReplaceAll(result, "@", "＠")
	return result
}

func run(msg M, img string, script string) {
	if !hasDockerImage(img) {
		nextMsg, _ := deepCopy(msg)
		nextMsg["mode"] = "reply"
		nextMsg["message"].(M)["text"] = "(DOWNLOAD " + img + " )"
		*ch <- nextMsg

		_ = pullDockerImage(img)
	}

	args := []string{"run", "--rm", "-i", "--log-driver", "none"}
	network := os.Getenv("LXBOT_ALLOW_DOCKER_NETWORK")
	if network != "true" {
		log.Println("[docker]", "resource limit:", "network=none")
		args = append(args, "--network", "none")
	}
	cpu := os.Getenv("LXBOT_ALLOW_DOCKER_UNLIMIT_CPU")
	if cpu != "true" {
		log.Println("[docker]", "resource limit:", "cpus=0.1")
		args = append(args, "--cpus", "0.1")
	}
	memory := os.Getenv("LXBOT_ALLOW_DOCKER_UNLIMIT_MEMORY")
	if memory != "true" {
		log.Println("[docker]", "resource limit:", "memory=128mb")
		args = append(args, "--memory", "128mb")
	}
	args = append(args, img)

	duration := 10 * time.Minute
	sec := os.Getenv("LXBOT_ALLOW_DOCKER_EXEC_DURATION_SEC")
	if sec != "" {
		s, err := strconv.Atoi(sec)
		if err == nil {
			log.Println("[docker]", "resource limit:", "exec duration="+sec+"sec")
			duration = time.Duration(s) * time.Second
		}
	} else {
		log.Println("[docker]", "resource limit:", "exec duration=10min")
	}

	cmd := exec.Command("docker", args...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	cmd.Start()

	stdoutBuff := buff.NewBuff()
	stderrBuff := buff.NewBuff()
	buffCh := make(chan int)
	cmdCh := make(chan error)
	endCh := make(chan int)

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
		cmdCh <- cmd.Wait()
	}()
	go func() {
		waitCh := time.After(3 * time.Second)

		for {
			select {
			case <- cmdCh:
				stdoutLines := stdoutBuff.DequeueALL()
				stderrLines := stderrBuff.DequeueALL()
				tag := "(FINISH)"
				text := generateText(stdoutLines, stderrLines, tag)
				if text == "" {
					text = tag
				}

				// FIXME: copy error
				nextMsg, _ := deepCopy(msg)
				nextMsg["mode"] = "reply"
				nextMsg["message"].(M)["text"] = text
				*ch <- nextMsg

				endCh <- 1
				return
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
				waitCh = time.After(5 * time.Second)

				stdoutLines := stdoutBuff.DequeueALL()
				stderrLines := stderrBuff.DequeueALL()
				text := generateText(stdoutLines, stderrLines, "(PARTIAL)")
				if text == "" {
					break
				}

				// FIXME: copy error
				nextMsg, _ := deepCopy(msg)
				nextMsg["mode"] = "reply"
				nextMsg["message"].(M)["text"] = text
				*ch <- nextMsg
				break
			case <-time.After(duration):
				if !cmd.ProcessState.Exited() {
					_ = cmd.Process.Kill()
				}
				stdoutLines := stdoutBuff.DequeueALL()
				stderrLines := stderrBuff.DequeueALL()
				text := generateText(stdoutLines, stderrLines, "(TIMEOUT)")

				// FIXME: copy error
				nextMsg, _ := deepCopy(msg)
				nextMsg["mode"] = "reply"
				nextMsg["message"].(M)["text"] = text
				*ch <- nextMsg

				endCh <- 1
				return
			}
		}
	}()

	_, _ = stdin.Write([]byte(script))
	_ = stdin.Close()

	wg.Wait()
	<- endCh
	close(buffCh)
	close(cmdCh)
	close(endCh)
}

func hasDockerImage(repo string) bool {
	out, err := exec.Command("docker","images", repo, "-q").Output()
	if err != nil {
		return false
	}
	return string(out) != ""
}


func pullDockerImage(repo string) error {
	return exec.Command("docker","pull", repo).Run()
}