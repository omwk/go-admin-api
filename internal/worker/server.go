package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type TaskPayload struct {
	TenantID int64  `json:"tenant_id"`
	JobID    int64  `json:"job_id"`
	RunID    int64  `json:"run_id"`
	Trigger  string `json:"trigger"`
}

func (p TaskPayload) MarshalJSON() ([]byte, error) {
	if err := validateTaskPayload(p); err != nil {
		return nil, err
	}
	if p.RunID < 0 {
		return nil, errors.New("worker run identifier is invalid")
	}
	type payload TaskPayload
	return json.Marshal(payload(p))
}

func validateTaskPayload(payload TaskPayload) error {
	if payload.TenantID <= 0 || payload.JobID <= 0 || payload.RunID < 0 || payload.Trigger == "" {
		return errors.New("worker task identifiers are required")
	}
	switch payload.Trigger {
	case "cron", "manual", "webhook", "retry":
		return nil
	default:
		return errors.New("worker task trigger is invalid")
	}
}

func DecodeTaskPayload(raw []byte) (TaskPayload, error) {
	var payload TaskPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return TaskPayload{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return TaskPayload{}, errors.New("worker task payload has trailing data")
		}
		return TaskPayload{}, err
	}
	if err := validateTaskPayload(payload); err != nil {
		return TaskPayload{}, err
	}
	return payload, nil
}

type Handler func(context.Context, TaskPayload) error

// TaskSource is the small adapter boundary for queue clients such as asynq.
// Receive must return when its context is cancelled.
type TaskSource interface {
	Receive(context.Context) (TaskPayload, error)
	Close() error
}

type Server struct {
	handler Handler
	source  TaskSource
	stop    chan struct{}
	mu      sync.Mutex
	cancel  context.CancelFunc
}

func NewServer(handler Handler) (*Server, error) { return NewServerWithSource(handler, nil) }

func NewServerWithSource(handler Handler, source TaskSource) (*Server, error) {
	if handler == nil {
		return nil, errors.New("worker handler is required")
	}
	return &Server{handler: handler, source: source, stop: make(chan struct{})}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stopSignal := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stopSignal()
	receiveCtx, cancelReceive := context.WithCancel(ctx)
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancel = func() {
		cancelReceive()
		cancelWork()
	}
	s.mu.Unlock()
	defer func() {
		cancelReceive()
		cancelWork()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()
	if s.source == nil {
		select {
		case <-ctx.Done():
		case <-s.stop:
		}
		return nil
	}
	return s.runSource(ctx, receiveCtx, workCtx, cancelReceive, cancelWork)
}

func (s *Server) runSource(ctx, receiveCtx, workCtx context.Context, cancelReceive, cancelWork context.CancelFunc) error {
	var running sync.WaitGroup
	workerErrors := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			payload, err := s.source.Receive(receiveCtx)
			if err != nil {
				if receiveCtx.Err() != nil {
					return
				}
				select {
				case workerErrors <- fmt.Errorf("receive worker task: %w", err):
				default:
				}
				cancelReceive()
				cancelWork()
				return
			}
			running.Add(1)
			go func() {
				defer running.Done()
				if err := s.handler(workCtx, payload); err != nil {
					select {
					case workerErrors <- fmt.Errorf("handle worker task: %w", err):
					default:
					}
					cancelReceive()
					cancelWork()
				}
			}()
		}
	}()
	var runErr error
	select {
	case <-ctx.Done():
	case <-s.stop:
	case err := <-workerErrors:
		runErr = err
	}
	cancelReceive()
	return s.shutdownSource(cancelWork, running.Wait, done, workerErrors, runErr)
}

func (s *Server) shutdownSource(cancelWork context.CancelFunc, waitRunning func(), done <-chan struct{}, workerErrors <-chan error, runErr error) error {
	shutdownDeadline := time.NewTimer(30 * time.Second)
	defer shutdownDeadline.Stop()
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.source.Close() }()
	var closeErr error
	select {
	case closeErr = <-closeDone:
	case <-shutdownDeadline.C:
		cancelWork()
		return errors.New("worker shutdown exceeded 30 seconds")
	}
	select {
	case <-done:
	case <-shutdownDeadline.C:
		cancelWork()
		return errors.New("worker shutdown exceeded 30 seconds")
	}
	finished := make(chan struct{})
	go func() { waitRunning(); close(finished) }()
	select {
	case <-finished:
		if runErr == nil {
			select {
			case runErr = <-workerErrors:
			default:
			}
		}
		if runErr != nil && closeErr != nil {
			return errors.Join(runErr, closeErr)
		}
		if runErr != nil {
			return runErr
		}
		return closeErr
	case <-shutdownDeadline.C:
		cancelWork()
		return errors.New("worker shutdown exceeded 30 seconds")
	}
}

func (s *Server) Shutdown() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}
