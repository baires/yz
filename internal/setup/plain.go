package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type plain struct {
	scanner *bufio.Scanner
	input   io.Reader
	output  io.Writer
}

func NewPlain(input io.Reader, output io.Writer) Interaction {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 4096)
	return &plain{scanner: scanner, input: input, output: output}
}

func (p *plain) Status(Step, string) {}

func (p *plain) Notice(msg string) {
	_, _ = fmt.Fprintln(p.output, msg)
}

func (p *plain) Write(b []byte) (int, error) {
	return p.output.Write(b)
}

func (p *plain) Choose(ctx context.Context, prompt string, choices []Choice) (string, error) {
	for i, choice := range choices {
		if choice.Description != "" {
			_, _ = fmt.Fprintf(p.output, "  %d. %s (%s)\n", i+1, choice.Title, choice.Description)
		} else {
			_, _ = fmt.Fprintf(p.output, "  %d. %s\n", i+1, choice.Title)
		}
	}
	answer, err := p.line(ctx, prompt, false)
	if err != nil {
		return "", err
	}
	number, err := strconv.Atoi(answer)
	if err != nil || number < 1 || number > len(choices) {
		if strings.Contains(prompt, "bucket") {
			return "", errors.New("invalid bucket selection; run yz setup and choose a listed number")
		}
		return "", errors.New("invalid account selection; choose a listed number")
	}
	return choices[number-1].Value, nil
}

func (p *plain) Text(ctx context.Context, req TextRequest) (string, error) {
	value, err := p.line(ctx, req.Label, req.Secret)
	if err != nil {
		return "", err
	}
	if req.Validate != nil {
		if err := req.Validate(value); err != nil {
			return "", err
		}
	}
	return value, nil
}

func (p *plain) ConfirmPublic(ctx context.Context) (bool, error) {
	answer, err := p.line(ctx, "Enable public access via r2.dev? Anyone with a URL can read shared files. [y/N]", false)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, errors.New("public access answer must be yes or no; run yz setup")
	}
}

func (p *plain) Retry(_ context.Context, msg string) (bool, error) {
	return false, errors.New(msg)
}

func (p *plain) keepsPlainErrors() {}

func (p *plain) line(ctx context.Context, label string, secret bool) (string, error) {
	if secret {
		restore, err := hideTerminalEcho(p.input)
		if err != nil {
			return "", err
		}
		defer restore()
	}
	_, _ = fmt.Fprintln(p.output, label)
	type answer struct {
		value string
		err   error
	}
	answers := make(chan answer, 1)
	go func() {
		if !p.scanner.Scan() {
			err := errors.New("setup input ended; run yz setup and answer the prompts")
			if p.scanner.Err() != nil {
				err = errors.New("setup input is invalid or exceeds 4096 bytes; run yz setup")
			}
			answers <- answer{err: err}
			return
		}
		answers <- answer{value: strings.TrimSpace(p.scanner.Text())}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case got := <-answers:
		return got.value, got.err
	}
}

func hideTerminalEcho(input io.Reader) (func(), error) {
	file, ok := input.(*os.File)
	if !ok {
		return func() {}, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.New("cannot inspect setup input")
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return func() {}, nil
	}
	get := exec.Command("stty", "-g")
	get.Stdin = file
	mode, err := get.Output()
	if err != nil {
		return nil, errors.New("cannot read terminal settings; pipe setup answers through stdin instead")
	}
	set := exec.Command("stty", "-echo")
	set.Stdin = file
	if err := set.Run(); err != nil {
		return nil, errors.New("cannot hide credential input; pipe setup answers through stdin instead")
	}
	return func() {
		restore := exec.Command("stty", strings.TrimSpace(string(mode)))
		restore.Stdin = file
		_ = restore.Run()
	}, nil
}
