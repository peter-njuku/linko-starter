package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"boot.dev/linko/internal/store"
	pkgerr "github.com/pkg/errors"
)

type stackTracer interface {
	error
	StackTrace() pkgerr.StackTrace
}

type closeFunc func() error

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()
	os.Exit(status)
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	logger, closeLog, err := initializeLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		return 1
	}

	defer func() {
		if err := closeLog(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close logger: %v\n", err)
		}
	}()

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to create store: %v\n", err))
		return 1
	}
	s := newServer(*st, httpPort, cancel, logger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logger.Debug("Linko is shutting down")
	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Error(fmt.Sprintf("failed to shutdown server: %v\n", err))
		return 1
	}
	if serverErr != nil {
		logger.Error(fmt.Sprintf("server error: %v\n", serverErr))
		return 1
	}
	return 0
}

func initializeLogger() (*slog.Logger, closeFunc, error) {
	//	var writers []io.Writer
	var closers []closeFunc
	var fileHandler slog.Handler
	var handlers []slog.Handler

	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: replaceAttr,
	})
	//	writers = append(writers, os.Stderr)
	closers = append(closers, func() error { return nil })

	if logFile := os.Getenv("LINKO_LOG_FILE"); logFile != "" {
		file, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open log file: %w", err)
		}

		//bufferedWriters := bufio.NewWriterSize(file, 8192)
		//		writers = append(writers, bufferedWriters)

		fileHandler = slog.NewJSONHandler(file, &slog.HandlerOptions{
			Level:       slog.LevelInfo,
			ReplaceAttr: replaceAttr,
		})

		closers = append(closers, func() error {
			//if err := bufferedWriters.Flush(); err != nil {
			//	file.Close()
			//	return fmt.Errorf("failed to flush log buffer: %w", err)
			//}
			return file.Close()
		})
	}

	handlers = append(handlers, stderrHandler)
	if fileHandler != nil {
		handlers = append(handlers, fileHandler)
	}

	multihandler := slog.NewMultiHandler(handlers...)

	//	multiwriter := io.MultiWriter(writers...)
	logger := slog.New(multihandler)

	return logger, func() error {
		var lastErr error
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i](); err != nil {
				lastErr = err
			}
		}
		return lastErr
	}, nil
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == "error" {
		err, ok := a.Value.Any().(error)
		if !ok {
			return a
		}
		if stackErr, ok := errors.AsType[stackTracer](err); ok {
			return slog.GroupAttrs(
				"error", slog.Attr{
					Key:   "message",
					Value: slog.StringValue(stackErr.Error()),
				}, slog.Attr{
					Key:   "stack_trace",
					Value: slog.StringValue(fmt.Sprintf("%+v", stackErr.StackTrace())),
				},
			)
		}
		return slog.String("error", fmt.Sprintf("%+v", err))
	}
	return a
}
