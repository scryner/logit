package logg

import (
	"compress/gzip"
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

	// log rotate related
	closer   io.Closer
	maxSize  int64
	enableGz bool
	filepath string

	written int64
}

type logToken struct {
	logger *Logger
	msg    string

	ch chan int
}

func startLoggerActor() {
	ready := make(chan bool)
	replacer := strings.NewReplacer("\n", "\n             ")

	go func(actor_in chan *logToken) {
		ready <- true

		for {
			token := <-actor_in

			logger := token.logger
			msg := replacer.Replace(token.msg)
			ch := token.ch

			if logger != nil {
				logger.refresh()

				if logger.l != nil {
					logger.l.Println(msg)
					logger.written += int64(len(msg))
				}
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

func newLogger(prefix string, allowedLogLevel LogLevel) *Logger {
	switch allowedLogLevel {
	case LOG_LEVEL_DEBUG, LOG_LEVEL_ERROR, LOG_LEVEL_FATAL, LOG_LEVEL_INFO, LOG_LEVEL_WARN:
		break
	default:
		allowedLogLevel = LOG_LEVEL_DEBUG
	}

	logger := new(Logger)

	logger.level = allowedLogLevel

	var newprefix string
	if prefix == "" {
		newprefix = ""
	} else {
		newprefix = fmt.Sprintf("[%-10s] ", prefix)
	}

	logger.prefix = newprefix

	return logger
}

func NewLogger(prefix string, w io.Writer, allowedLogLevel LogLevel) *Logger {
	logger := newLogger(prefix, allowedLogLevel)

	logger.l = golog.New(w, logger.prefix, golog.Ldate|golog.Lmicroseconds)
	logger.closer = nil
	logger.maxSize = -1
	logger.written = 0
	logger.enableGz = false

	return logger
}

func NewFileLogger(prefix string, filepath string, allowedLogLevel LogLevel, maxSize int64, enableGz bool) (*Logger, error) {
	if maxSize < 0 {
		maxSize = -1
	}

	f, err := os.OpenFile(filepath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	logger := newLogger(prefix, allowedLogLevel)

	logger.l = golog.New(f, logger.prefix, golog.Ldate|golog.Lmicroseconds)
	logger.closer = f
	logger.maxSize = maxSize
	logger.written = fi.Size()
	logger.enableGz = enableGz
	logger.filepath = filepath

	return logger, nil
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

	token.logger = logger
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

func (logger *Logger) refresh() error {
	if logger.maxSize <= 0 || logger.written <= logger.maxSize {
		return nil
	}

	// close current stream
	if logger.closer != nil {
		safelyDo(func() {
			logger.closer.Close()
		})
	}

	// find latest file
	i := 0
	maxI := -1

	for {
		var err error

		if logger.enableGz {
			_, err = os.Stat(fmt.Sprintf("%s.%d.gz", logger.filepath, i))
		} else {
			_, err = os.Stat(fmt.Sprintf("%s.%d", logger.filepath, i))
		}

		if err == nil || os.IsExist(err) {
			maxI = i
		} else {
			break
		}

		i += 1
	}

	for i = maxI; i >= 0; i-- {
		var oldpath, newpath string

		if logger.enableGz {
			oldpath = fmt.Sprintf("%s.%d.gz", logger.filepath, i)
			newpath = fmt.Sprintf("%s.%d.gz", logger.filepath, i+1)
		} else {
			oldpath = fmt.Sprintf("%s.%d", logger.filepath, i)
			newpath = fmt.Sprintf("%s.%d", logger.filepath, i+1)
		}

		os.Rename(oldpath, newpath)
	}

	// rename current file to .0 file
	os.Rename(logger.filepath, fmt.Sprintf("%s.0", logger.filepath))

	// gzip if necessary
	if logger.enableGz {
		go func() {
			oldpath := fmt.Sprintf("%s.0", logger.filepath)
			newpath := fmt.Sprintf("%s.gz", oldpath)

			f, err := os.Open(oldpath)
			if err != nil {
				return
			}

			defer func() {
				f.Close()
				os.Remove(oldpath)
			}()

			w, err := os.OpenFile(newpath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return
			}

			defer w.Close()

			gw := gzip.NewWriter(w)
			defer gw.Close()

			io.Copy(gw, f)
		}()
	}

	// new open stream
	f, err := os.OpenFile(logger.filepath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	logger.l = golog.New(f, logger.prefix, golog.Ldate|golog.Lmicroseconds)
	logger.closer = f
	logger.written = 0

	return nil
}

func (logger *Logger) GetCloser() io.Closer {
	return logger.closer
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

	//s := fmt.Sprintf(format, v...)
	//panic(s)
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

func safelyDo(fun func()) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	fun()
	return
}
