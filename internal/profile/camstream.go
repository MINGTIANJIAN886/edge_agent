package profile

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// maxFrameBytes guards against corrupt length prefixes.
const maxFrameBytes = 8 << 20

// CameraStream manages a single persistent camera capture process
// (python + cv2) and fans its JPEG frames out to HTTP subscribers as
// MJPEG. Only one stream can run at a time; while running, the camera
// probe will report the device as busy, which is truthful behavior.
//
// The process is supervised: if it dies unexpectedly (e.g. USB
// re-enumeration invalidates the device node) and streaming is still
// wanted, it is automatically restarted after a short backoff so
// viewers keep receiving frames.
type CameraStream struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	wantRunning bool
	subs        map[chan []byte]struct{}
	latest      []byte
	script      string
	device      string
	fps         int
}

const restartBackoff = 2 * time.Second

func NewCameraStream(script, device string, fps int) *CameraStream {
	return &CameraStream{
		subs:   make(map[chan []byte]struct{}),
		script: script,
		device: device,
		fps:    fps,
	}
}

func (s *CameraStream) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

func (s *CameraStream) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return fmt.Errorf("camera stream already running")
	}
	s.wantRunning = true
	return s.spawnLocked()
}

func (s *CameraStream) Stop() {
	s.mu.Lock()
	s.wantRunning = false
	cancel := s.cancel
	cmd := s.cmd
	s.cmd = nil
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		cmd.Process.Kill()
	}
}

// spawnLocked starts the capture process. The mutex must be held.
func (s *CameraStream) spawnLocked() error {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "python3", s.script,
		"--device", s.device,
		"--fps", fmt.Sprintf("%d", s.fps),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmdStderr{}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start camera stream: %w", err)
	}
	s.cmd = cmd
	s.cancel = cancel
	s.latest = nil
	go s.readLoop(ctx, stdout)
	go s.supervise(ctx, cmd)
	log.Printf("camera stream: started (device=%s fps=%d)", s.device, s.fps)
	return nil
}

// supervise waits for the capture process and auto-restarts it as long
// as streaming is still wanted.
func (s *CameraStream) supervise(ctx context.Context, cmd *exec.Cmd) {
	err := cmd.Wait()
	if err != nil && ctx.Err() == nil {
		log.Printf("camera stream: process exited: %v", err)
	}

	s.mu.Lock()
	isCurrent := s.cmd == cmd
	if isCurrent {
		s.cmd = nil
		s.cancel = nil
	}
	want := s.wantRunning
	s.mu.Unlock()
	s.broadcast(nil)
	log.Printf("camera stream: stopped (restart=%v)", want)

	if !isCurrent || !want || ctx.Err() != nil {
		return
	}
	time.Sleep(restartBackoff)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wantRunning && s.cmd == nil {
		log.Printf("camera stream: restarting after unexpected exit")
		s.spawnLocked()
	}
}

func (s *CameraStream) readLoop(ctx context.Context, r io.Reader) {
	br := bufio.NewReaderSize(r, 256<<10)
	var lenBuf [4]byte
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			return
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])
		if n == 0 || n > maxFrameBytes {
			log.Printf("camera stream: invalid frame length %d", n)
			return
		}
		frame := make([]byte, n)
		if _, err := io.ReadFull(br, frame); err != nil {
			return
		}
		s.mu.Lock()
		s.latest = frame
		s.mu.Unlock()
		s.broadcast(frame)
	}
}

func (s *CameraStream) broadcast(frame []byte) {
	s.mu.Lock()
	subs := make([]chan []byte, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (s *CameraStream) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	unsub := func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
	return ch, unsub
}

// NumSubscribers returns the current subscriber count.
func (s *CameraStream) NumSubscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// Snapshot returns the most recent frame, or nil if none yet.
func (s *CameraStream) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

type cmdStderr struct{}

func (cmdStderr) Write(p []byte) (int, error) {
	log.Printf("camera stream stderr: %s", string(p))
	return len(p), nil
}
