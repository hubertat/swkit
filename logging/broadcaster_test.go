package logging

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBroadcasterWritesToPrimary(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	msg := "hello world\n"
	n, err := bc.Write([]byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(msg) {
		t.Fatalf("expected %d bytes written, got %d", len(msg), n)
	}
	if buf.String() != msg {
		t.Fatalf("expected primary to contain %q, got %q", msg, buf.String())
	}
}

func TestBroadcasterSubscribeReceivesLines(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	ch, unsub := bc.Subscribe(FormatANSI)
	defer unsub()

	bc.Write([]byte("line one\nline two\n"))

	line1 := readWithTimeout(t, ch, time.Second)
	line2 := readWithTimeout(t, ch, time.Second)

	if string(line1) != "line one\n" {
		t.Fatalf("expected %q, got %q", "line one\n", string(line1))
	}
	if string(line2) != "line two\n" {
		t.Fatalf("expected %q, got %q", "line two\n", string(line2))
	}
}

func TestBroadcasterReplay(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	// Write lines before subscribing.
	bc.Write([]byte("before\n"))

	ch, unsub := bc.Subscribe(FormatANSI)
	defer unsub()

	// Should get replay.
	line := readWithTimeout(t, ch, time.Second)
	if string(line) != "before\n" {
		t.Fatalf("expected replay of %q, got %q", "before\n", string(line))
	}
}

func TestBroadcasterRingOverflow(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 3)

	// Write 5 lines — ring keeps last 3.
	for i := 0; i < 5; i++ {
		bc.Write([]byte("line" + string(rune('A'+i)) + "\n"))
	}

	ch, unsub := bc.Subscribe(FormatANSI)
	defer unsub()

	var got []string
	for i := 0; i < 3; i++ {
		line := readWithTimeout(t, ch, time.Second)
		got = append(got, strings.TrimSpace(string(line)))
	}

	expected := []string{"lineC", "lineD", "lineE"}
	for i, exp := range expected {
		if got[i] != exp {
			t.Errorf("replay[%d]: expected %q, got %q", i, exp, got[i])
		}
	}
}

func TestBroadcasterFormatPlainStripsANSI(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	ch, unsub := bc.Subscribe(FormatPlain)
	defer unsub()

	// Write a line with ANSI color codes.
	bc.Write([]byte("\033[31mred text\033[0m\n"))

	line := readWithTimeout(t, ch, time.Second)
	if strings.Contains(string(line), "\033") {
		t.Fatalf("expected ANSI to be stripped, got %q", string(line))
	}
	if strings.TrimSpace(string(line)) != "red text" {
		t.Fatalf("expected 'red text', got %q", strings.TrimSpace(string(line)))
	}
}

func TestBroadcasterPartialLines(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	ch, unsub := bc.Subscribe(FormatANSI)
	defer unsub()

	// Write partial line, then complete it.
	bc.Write([]byte("hel"))
	bc.Write([]byte("lo\n"))

	line := readWithTimeout(t, ch, time.Second)
	if string(line) != "hello\n" {
		t.Fatalf("expected %q, got %q", "hello\n", string(line))
	}
}

func TestBroadcasterSlowSubscriberDropsLines(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	// Subscribe with tiny buffer (channel size is 256 in Subscribe, so we use the internal mechanism).
	ch, unsub := bc.Subscribe(FormatANSI)
	defer unsub()

	// Write more lines than the channel buffer can hold.
	for i := 0; i < 300; i++ {
		bc.Write([]byte("flood line\n"))
	}

	// Should be able to drain some lines without blocking.
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	// We should have received some but not necessarily all 300.
	if count == 0 {
		t.Fatal("expected to receive at least some lines")
	}
	if count > 300 {
		t.Fatalf("received more lines than written: %d", count)
	}
}

func TestBroadcasterUnsubscribe(t *testing.T) {
	var buf bytes.Buffer
	bc := NewBroadcaster(&buf, 10)

	ch, unsub := bc.Subscribe(FormatANSI)
	unsub()

	bc.Write([]byte("after unsub\n"))

	// After unsubscribe, no new lines should appear on the channel.
	select {
	case <-ch:
		t.Fatal("expected no lines after unsub")
	case <-time.After(50 * time.Millisecond):
		// Good — nothing received.
	}
}

func readWithTimeout(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case line := <-ch:
		return line
	case <-time.After(timeout):
		t.Fatal("timed out waiting for line")
		return nil
	}
}
