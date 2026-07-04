package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const (
	LevelDebug = iota
	LevelInfo
	LevelWarn
	LevelError
)

var (
	level     = LevelInfo
	debugLog  = log.New(os.Stdout, "[DEBUG] ", log.LstdFlags)
	infoLog   = log.New(os.Stdout, "[INFO]  ", log.LstdFlags)
	warnLog   = log.New(os.Stdout, "[WARN]  ", log.LstdFlags)
	errorLog  = log.New(os.Stderr, "[ERROR] ", log.LstdFlags)
)

func SetLevel(levelName string) {
	switch strings.ToLower(strings.TrimSpace(levelName)) {
	case "debug":
		level = LevelDebug
	case "info":
		level = LevelInfo
	case "warn":
		level = LevelWarn
	case "error":
		level = LevelError
	default:
		level = LevelInfo
	}
}

func LevelName() string {
	switch level {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "unknown"
	}
}

func SetOutput(w io.Writer) {
	debugLog.SetOutput(w)
	infoLog.SetOutput(w)
	warnLog.SetOutput(w)
	errorLog.SetOutput(w)
}

func Debug(format string, v ...any) {
	if level <= LevelDebug {
		debugLog.Output(2, fmt.Sprintf(format, v...))
	}
}

func Info(format string, v ...any) {
	if level <= LevelInfo {
		infoLog.Output(2, fmt.Sprintf(format, v...))
	}
}

func Warn(format string, v ...any) {
	if level <= LevelWarn {
		warnLog.Output(2, fmt.Sprintf(format, v...))
	}
}

func Error(format string, v ...any) {
	if level <= LevelError {
		errorLog.Output(2, fmt.Sprintf(format, v...))
	}
}

func Fatal(format string, v ...any) {
	errorLog.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}