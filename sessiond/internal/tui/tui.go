package tui

import (
	"bufio"
	"fmt"
	"os"
	"unicode/utf8"

	"PrismPanel-daemon/sessionproto"
	"PrismPanel-sessiond/internal/client"
	"golang.org/x/term"
)

func Run(manager *client.Manager) error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)
	index := 0
	for {
		sessions, err := manager.List()
		if err != nil {
			return err
		}
		if index >= len(sessions) {
			index = len(sessions) - 1
		}
		if index < 0 {
			index = 0
		}
		renderList(sessions, index)
		key, err := readKey()
		if err != nil {
			return nil
		}
		switch key {
		case "q", "ctrl-c":
			return nil
		case "down":
			if index+1 < len(sessions) {
				index++
			}
		case "up":
			if index > 0 {
				index--
			}
		case "enter":
			if len(sessions) == 0 {
				continue
			}
			if err := attachConsole(sessions[index]); err != nil {
				fmt.Fprintf(os.Stdout, "attach failed: %v\r\n", err)
			}
		}
	}
}

func renderList(sessions []sessionproto.State, index int) {
	fmt.Fprint(os.Stdout, "\x1b[2J\x1b[H")
	fmt.Fprintln(os.Stdout, "Prism Session")
	fmt.Fprintln(os.Stdout, "Up/Down select  Enter attach  Esc/q quit")
	fmt.Fprintln(os.Stdout)
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stdout, "(no running sessions)")
		return
	}
	for i, item := range sessions {
		prefix := "  "
		if i == index {
			prefix = "> "
		}
		fmt.Fprintf(os.Stdout, "%s%s\r\n", prefix, client.FormatState(item))
	}
}

func attachConsole(state sessionproto.State) error {
	conn, err := sessionproto.Dial(state.Socket, 0)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeHello, Token: state.Token}); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	if _, err := sessionproto.ReadFrame(reader); err != nil {
		return err
	}
	_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeAttach})
	fmt.Fprintf(os.Stdout, "\x1b[2J\x1b[Hattached %s  type a line to send, Esc returns\r\n", state.InstanceID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			frame, err := sessionproto.ReadFrame(reader)
			if err != nil {
				return
			}
			if len(frame.ContentBytes) > 0 {
				fmt.Fprintf(os.Stdout, "%s\r\n", string(frame.ContentBytes))
			} else if frame.Content != "" {
				fmt.Fprintf(os.Stdout, "%s\r\n", frame.Content)
			}
		}
	}()
	var line []byte
	for {
		select {
		default:
		}
		key, err := readKey()
		if err != nil {
			return nil
		}
		switch key {
		case "esc", "ctrl-c":
			return nil
		case "enter":
			if err := sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeStdin, Content: string(line) + "\n"}); err != nil {
				return err
			}
			line = line[:0]
		case "backspace":
			if len(line) > 0 {
				_, size := utf8.DecodeLastRune(line)
				line = line[:len(line)-size]
			}
		default:
			if key != "up" && key != "down" && key != "q" {
				line = append(line, key...)
			}
		}
	}
}

func readKey() (string, error) {
	buffer := make([]byte, 8)
	n, err := os.Stdin.Read(buffer)
	if err != nil {
		return "", err
	}
	switch {
	case n == 1 && buffer[0] == 3:
		return "ctrl-c", nil
	case n == 1 && buffer[0] == 27:
		return "esc", nil
	case n == 1 && (buffer[0] == 13 || buffer[0] == 10):
		return "enter", nil
	case n == 1 && (buffer[0] == 127 || buffer[0] == 8):
		return "backspace", nil
	case n >= 3 && buffer[0] == 27 && buffer[1] == '[' && buffer[2] == 'A':
		return "up", nil
	case n >= 3 && buffer[0] == 27 && buffer[1] == '[' && buffer[2] == 'B':
		return "down", nil
	case n == 1 && buffer[0] == 'q':
		return "q", nil
	default:
		return string(buffer[:n]), nil
	}
}
