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

	// global variable
	lock *sync.Mutex

	loggers map[string]*logg.Logger
	fds     []io.Closer
)

func init() {
	flag.IntVar(&listenPort, "p", 8070, "listen port")
	flag.StringVar(&logFilePath, "w", "", "log file path")
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
	var dw io.Writer
	var dwPrefix string

	if logFilePath == "" {
		dw = os.Stdout
		dwPrefix = "logit"

	} else {
		f, err := os.OpenFile(fmt.Sprintf("%s/logit.log", logFilePath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("can't open default log file: %v", err)
		}

		dw = f
		dwPrefix = ""

		fds = append(fds, f)
	}

	logger := logg.NewLogger(dwPrefix, dw, logg.LOG_LEVEL_DEBUG)

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
			var w io.Writer
			var prefix string

			if logFilePath == "" {
				w = os.Stdout
				prefix = lowerSender

			} else {
				f, err := os.OpenFile(fmt.Sprintf("%s/%s.log", logFilePath, lowerSender), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					w = os.Stdout
					prefix = lowerSender
				} else {
					w = f
					prefix = ""

					fds = append(fds, f)
				}
			}

			senderLogger = logg.NewLogger(prefix, w, logg.LOG_LEVEL_DEBUG)

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
			fmt.Fprintf(os.Stderr, "log file path stat failed: %v", err)
			os.Exit(1)
		}

		if !finfo.IsDir() {
			fmt.Fprintf(os.Stderr, "log file path must be directory")
			os.Exit(1)
		}

		logFilePath, err = filepath.Abs(logFilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "log file path converting failed: %v", err)
			os.Exit(1)
		}
	}

	handler, err := makeHandler(logFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log file handler initialization failed: %v", err)
		os.Exit(1)
	}

	http.HandleFunc("/", handler)

	fmt.Printf("logit server starting at port '%d'\n", listenPort)
	http.ListenAndServe(fmt.Sprintf(":%d", listenPort), nil)
}
