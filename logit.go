package main

import (
	"flag"
	"fmt"
	"github.com/scryner/logg"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// flags
	listenPort  int
	logFilePath string

	enableGz   bool
	maxSizeStr string
	maxSize    int64

	// global variable
	lock *sync.Mutex

	loggers map[string]*logg.Logger
	fds     []io.Closer
)

func init() {
	flag.IntVar(&listenPort, "p", 8070, "listen port")
	flag.StringVar(&logFilePath, "w", "", "log file path")
	flag.StringVar(&maxSizeStr, "s", "16m", "max size (-1 means no log rotation)")
	flag.BoolVar(&enableGz, "z", true, "enable gz")
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

func makeHandler(logFilePath string) (http.HandlerFunc, error) {
	var logger *logg.Logger

	if logFilePath == "" {
		logger = logg.NewLogger("logit", os.Stdout, logg.LOG_LEVEL_DEBUG)

	} else {
		var err error

		logger, err = logg.NewFileLogger("", fmt.Sprintf("%s/logit.log", logFilePath), logg.LOG_LEVEL_DEBUG, maxSize, enableGz)
		if err != nil {
			return nil, fmt.Errorf("can't open default log file: %v", err)
		}

		fds = append(fds, logger.GetCloser())
	}

	return func(rw http.ResponseWriter, req *http.Request) {
		defer func() {
			// just return blank content
			fmt.Fprintf(rw, "")
		}()

		// read body
		b, err := ioutil.ReadAll(req.Body)
		if err != nil {
			logger.Errorf("body read failed: %v", err)
			return
		}
		err = safelyDo(func() {
			req.Body.Close()
		})
		if err != nil {
			logger.Warnf("body close failed: %v", err)
		}

		content := string(b)

		// get parameters
		ss := strings.Split(req.RequestURI, "/")

		if len(ss) < 2 {
			logger.Errorf("wrong sender: %v / %s", req.RequestURI, content)
			return
		}

		sender := strings.TrimSpace(ss[1])

		if sender == "" {
			logger.Errorf("wrong sender: %v / %s", req.RequestURI, content)
			return
		}

		var logLevel string

		if len(ss) < 3 {
			logLevel = "debug"
		} else {
			logLevel = ss[2]
		}

		// find logger
		lowerSender := strings.ToLower(sender)
		senderLogger := loggers[lowerSender]
		if senderLogger == nil {
			// create new logger
			if logFilePath == "" {
				senderLogger = logg.NewLogger(lowerSender, os.Stdout, logg.LOG_LEVEL_DEBUG)

			} else {
				senderLogger, err = logg.NewFileLogger("", fmt.Sprintf("%s/%s.log", logFilePath, lowerSender), logg.LOG_LEVEL_DEBUG, maxSize, enableGz)
				if err != nil {
					senderLogger = logg.NewLogger(lowerSender, os.Stdout, logg.LOG_LEVEL_DEBUG)
				} else {
					fds = append(fds, senderLogger.GetCloser())
				}
			}

			lock.Lock()
			loggers[lowerSender] = senderLogger
			lock.Unlock()
		}

		// log it
		logLevel = strings.TrimSpace(logLevel)
		logLevel = strings.ToLower(logLevel)

		switch logLevel {
		case "info":
			senderLogger.Infof("%s", content)

		case "warn":
			senderLogger.Warnf("%s", content)

		case "error":
			senderLogger.Errorf("%s", content)

		case "fatal":
			senderLogger.Fatalf("%s", content)

		default:
			senderLogger.Debugf("%s", content)
		}
	}, nil
}

func main() {
	flag.Parse()

	var suffix string

	fmt.Sscanf(maxSizeStr, "%d%s", &maxSize, &suffix)

	switch suffix {
	case "k", "K":
		maxSize *= 1024

	case "m", "M":
		maxSize *= 1024 * 1024

	case "g", "G":
		maxSize *= 1024 * 1024 * 1024
	}

	// initialize global variables
	lock = &sync.Mutex{}
	loggers = make(map[string]*logg.Logger)

	var err error

	// sig handler
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)

	go func() {
		for _ = range c {
			logg.Flush()

			for _, f := range fds {
				f.Close()
			}

			os.Exit(0)
		}
	}()

	if logFilePath != "" {
		finfo, err := os.Stat(logFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "log file path stat failed: %v\n", err)
			os.Exit(1)
		}

		if !finfo.IsDir() {
			fmt.Fprintf(os.Stderr, "log file path must be directory")
			os.Exit(1)
		}

		logFilePath, err = filepath.Abs(logFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "log file path converting failed: %v\n", err)
			os.Exit(1)
		}
	}

	handler, err := makeHandler(logFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log file handler initialization failed: %v\n", err)
		os.Exit(1)
	}

	http.HandleFunc("/", handler)

	fmt.Printf("logit server starting at port '%d'\n", listenPort)

	if logFilePath != "" {
		fmt.Printf("log file path: %s\n", logFilePath)
		fmt.Printf("log file max size: %d\n", maxSize)
		fmt.Printf("enable gzip: %v\n", enableGz)
	}

	http.ListenAndServe(fmt.Sprintf(":%d", listenPort), nil)
}
