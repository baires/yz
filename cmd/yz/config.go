package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/baires/yz/internal/config"
)

const configHelp = `usage: yz config [auto-update [on|off]]

Show or change yz settings.

  yz config                 Show settings
  yz config auto-update     Show whether background updates are on
  yz config auto-update on  Download newer releases after commands
  yz config auto-update off Turn background updates off

Background updates are off unless enabled here or with YZ_AUTO_UPDATE=1.
`

func runConfig(args []string, env envConfig, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("yz config", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { _, _ = fmt.Fprint(stderr, configHelp) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	switch flags.NArg() {
	case 0:
		on, err := autoSetting(env.ConfigDir)
		if err != nil {
			writeCmdErr(stderr, err)
			return 1
		}
		printAuto(stdout, on)
		return 0
	case 1:
		if flags.Arg(0) != "auto-update" {
			flags.Usage()
			return 2
		}
		on, err := autoSetting(env.ConfigDir)
		if err != nil {
			writeCmdErr(stderr, err)
			return 1
		}
		printAuto(stdout, on)
		return 0
	case 2:
		if flags.Arg(0) != "auto-update" {
			flags.Usage()
			return 2
		}
		on, ok := parseAuto(flags.Arg(1))
		if !ok {
			flags.Usage()
			return 2
		}
		if err := setAuto(env.ConfigDir, on); err != nil {
			writeCmdErr(stderr, err)
			return 1
		}
		printAuto(stdout, on)
		return 0
	default:
		flags.Usage()
		return 2
	}
}

func writeCmdErr(stderr io.Writer, err error) {
	msg := err.Error()
	if !strings.HasPrefix(msg, "yz:") {
		msg = "yz: " + msg
	}
	_, _ = fmt.Fprintln(stderr, msg)
}

func printAuto(w io.Writer, on bool) {
	state := "off"
	if on {
		state = "on"
	}
	_, _ = fmt.Fprintf(w, "auto-update: %s\n", state)
}

func parseAuto(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "on", "true":
		return true, true
	case "off", "false":
		return false, true
	default:
		return false, false
	}
}

func autoSetting(dir string) (bool, error) {
	cfg, missing, err := loadOptionalConfig(dir)
	if err != nil || missing {
		return false, err
	}
	return cfg.AutoUpdate, nil
}

func setAuto(dir string, on bool) error {
	if dir == "" {
		return errors.New("config directory is not set")
	}
	cfg, _, err := loadOptionalConfig(dir)
	if err != nil {
		return err
	}
	cfg.AutoUpdate = on
	return config.Save(dir, cfg)
}

// loadOptionalConfig returns a new config when the file does not exist yet.
// A present file is validated, including its permissions.
func loadOptionalConfig(dir string) (*config.Config, bool, error) {
	if dir == "" {
		return nil, false, errors.New("config directory is not set")
	}
	_, err := os.Stat(filepath.Join(dir, config.FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return &config.Config{}, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, false, err
	}
	return cfg, false, nil
}
