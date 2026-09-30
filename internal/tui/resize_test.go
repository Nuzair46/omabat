package tui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Exercise the real renderer without starting the database/live collector.
type resizeModel struct{ Model }

func (m resizeModel) Init() tea.Cmd { return nil }

func (m resizeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.Model.Update(msg)
	return resizeModel{next.(Model)}, cmd
}

type screenOutput struct {
	sync.Mutex
	buffer bytes.Buffer
	wrote  chan struct{}
}

func (o *screenOutput) Write(p []byte) (int, error) {
	o.Lock()
	n, err := o.buffer.Write(p)
	o.Unlock()
	select {
	case o.wrote <- struct{}{}:
	default:
	}
	return n, err
}

func (o *screenOutput) text() string {
	o.Lock()
	defer o.Unlock()
	return o.buffer.String()
}

func TestResizeClearsReflowedDashboard(t *testing.T) {
	out := &screenOutput{wrote: make(chan struct{}, 1)}
	model := resizeModel{Model{Width: 180, Height: 50, Range: 24 * time.Hour}}
	p := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(out), tea.WithAltScreen(), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	defer func() {
		p.Quit()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("renderer failed: %v", err)
			}
		case <-time.After(3 * time.Second):
			p.Kill()
			t.Error("renderer did not exit")
		}
	}()

	// The terminal may wrap the old wide frame into extra rows as the window
	// is tiled. Those rows are outside Bubble Tea's previous logical height.
	for _, width := range []int{180, 90, 45, 90} {
		start := len(out.text())
		p.Send(tea.WindowSizeMsg{Width: width, Height: 50})
		deadline := time.NewTimer(3 * time.Second)
		for {
			frame := out.text()[start:]
			clear := strings.LastIndex(frame, "\x1b[2J")
			// Verify a full erase followed by the new dashboard, rather than
			// only checking the model's command or its logical line count.
			if clear >= 0 && strings.Contains(frame[clear:], "Omabat") && strings.Contains(frame[clear:], "q quit") {
				deadline.Stop()
				break
			}
			select {
			case <-out.wrote:
			case <-deadline.C:
				t.Fatalf("resize to %d columns did not clear and redraw the dashboard; output: %q", width, frame)
			}
		}
	}
}
