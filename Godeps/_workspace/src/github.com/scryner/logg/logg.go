package logg

import (
	"fmt"
	"io"
	golog "log"
	"os"
	"strings"
)

type LogLevel int

const (
	_                        = iota
	LOG_LEVEL_DEBUG LogLevel = 1 << iota
	LOG_LEVEL_INFO
	LOG_LEVEL_WARN
	LOG_LEVEL_ERROR
	LOG_LEVEL_FATAL
)

const LOG_QUEUE = 1024

// global variable
var (
	actor_in          chan *logToken
	default_w         io.Writer
	default_log_level LogLevel
)

func init() {
	actor_in = make(chan *logToken, LOG_QUEUE) // when queue is full with queue size, caller would to wait sometime
	startLoggerActor()

	default_w = os.Stderr
	default_log_level = LOG_LEVEL_DEBUG
}

type Logger struct {
	level  LogLevel
	prefix string
	l      *golog.Logger
}

type logToken struct {
	l   *golog.Logger
	msg string

	ch chan int
}

func startLoggerActor() {
	ready := make(chan bool)
	replacer := strings.NewReplacer("\n", "\n             ")

	go func(actor_in chan *logToken) {
		ready <- true

		for {
			token := <-actor_in

			l := token.l
			msg := replacer.Replace(token.msg)
			ch := token.ch

			if l != nil {
				l.Println(msg)
			}

			if ch != nil {
				ch <- 1
			}
		}
	}(actor_in)

	<-ready
}

func LogLevelFrom(s string, defaultLevel LogLevel) (level LogLevel) {
	s2 := strings.ToLower(s)

	switch s2 {
	case "debug":
		level = LOG_LEVEL_DEBUG
	case "info":
		level = LOG_LEVEL_INFO
	case "warn":
		level = LOG_LEVEL_WARN
	case "error":
		level = LOG_LEVEL_ERROR
	case "fatal":
		level = LOG_LEVEL_FATAL
	default:
		level = defaultLevel
	}

	return
}

func NewLogger(prefix string, w io.Writer, allowedLogLevel LogLevel) *Logger {
	switch allowedLogLevel {
	case LOG_LEVEL_DEBUG, LOG_LEVEL_INFO, LOG_LEVEL_WARN, LOG_LEVEL_ERROR, LOG_LEVEL_FATAL:
	default:
		allowedLogLevel = LOG_LEVEL_DEBUG
	}

	logger := new(Logger)

	logger.level = allowedLogLevel
	logger.prefix = prefix

	var newprefix string
	if prefix == "" {
		newprefix = ""
	} else {
		newprefix = fmt.Sprintf("[%-10s] ", prefix)
	}

	logger.l = golog.New(w, newprefix, golog.Ldate|golog.Lmicroseconds)

	return logger
}

func SetDefaultLogger(w io.Writer, allowedLogLevel LogLevel) {
	default_log_level = allowedLogLevel
	default_w = w
}

func GetDefaultLogger(prefix string) *Logger {
	return NewLogger(prefix, default_w, default_log_level)
}

func newLogToken(logger *Logger, ch chan int, format string, v ...interface{}) (token *logToken) {
	token = new(logToken)

	token.l = logger.l
	token.msg = fmt.Sprintf(format, v...)
	token.ch = ch

	return
}

func (logger *Logger) _printf(level LogLevel, wait bool, format string, v ...interface{}) {
	if logger.level > level {
		return
	}

	if !wait {
		token := newLogToken(logger, nil, format, v...)
		actor_in <- token
	} else {
		ch := make(chan int)
		token := newLogToken(logger, ch, format, v...)
		actor_in <- token

		<-ch // wait to flush log
	}
}

func Flush() {
	ch := make(chan int)
	token := &logToken{nil, ``, ch} // logger == nil means just time to flush
	actor_in <- token

	<-ch // wait to flush log
}

func (logger *Logger) Printf(wait bool, format string, v ...interface{}) {
	logger._printf(logger.level, wait, format, v...)
}

func (logger *Logger) Debugf(format string, v ...interface{}) {
	newformat := setMessagePrefix(format, LOG_LEVEL_DEBUG)
	logger._printf(LOG_LEVEL_DEBUG, false, newformat, v...)
}

func (logger *Logger) Infof(format string, v ...interface{}) {
	newformat := setMessagePrefix(format, LOG_LEVEL_INFO)
	logger._printf(LOG_LEVEL_INFO, false, newformat, v...)
}

func (logger *Logger) Warnf(format string, v ...interface{}) {
	newformat := setMessagePrefix(format, LOG_LEVEL_WARN)
	logger._printf(LOG_LEVEL_WARN, false, newformat, v...)
}

func (logger *Logger) Errorf(format string, v ...interface{}) {
	newformat := setMessagePrefix(format, LOG_LEVEL_ERROR)
	logger._printf(LOG_LEVEL_ERROR, false, newformat, v...)
}

func (logger *Logger) Fatalf(format string, v ...interface{}) {
	newformat := setMessagePrefix(format, LOG_LEVEL_FATAL)
	logger._printf(LOG_LEVEL_FATAL, true, newformat, v...)

	s := fmt.Sprintf(format, v...)
	panic(s)
}

func setMessagePrefix(format string, level LogLevel) string {
	var msg_prefix string

	switch level {
	case LOG_LEVEL_DEBUG:
		msg_prefix = `(DEBG) `
	case LOG_LEVEL_INFO:
		msg_prefix = `(INFO) `
	case LOG_LEVEL_WARN:
		msg_prefix = `(WARN) `
	case LOG_LEVEL_ERROR:
		msg_prefix = `(ERRO) `
	case LOG_LEVEL_FATAL:
		msg_prefix = `(FATL) `
	}

	return fmt.Sprintf("%s%s", msg_prefix, format)
}
