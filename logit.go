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
	"strings"
	"sync"
)

var (
	// flags
	listenPort  int
	logFilePath string

	// global variable
	lock *sync.Mutex
	w    io.Writer

	loggers map[string]*logg.Logger
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

func makeHandler(w io.Writer) http.HandlerFunc {
	logger := logg.NewLogger("logit", w, logg.LOG_LEVEL_DEBUG)

	return func(rw http.ResponseWriter, req *http.Request) {
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
			senderLogger = logg.NewLogger(lowerSender, w, logg.LOG_LEVEL_DEBUG)
			loggers[lowerSender] = senderLogger
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
	}
}

func main() {
	flag.Parse()

	// initialize global variables
	lock = &sync.Mutex{}
	loggers = make(map[string]*logg.Logger)

	var f *os.File = nil
	var err error

	// sig handler
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)

	go func() {
		for _ = range c {
			logg.Flush()

			if f != nil {
				f.Close()
			}

			os.Exit(0)
		}
	}()

	defer func() {
		logg.Flush()
		os.Exit(0)
	}()

	if logFilePath == "" {
		w = os.Stdout
	} else {
		f, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "can't open log file '%s'\n", logFilePath)
			os.Exit(1)
		}

		w = f
	}

	http.HandleFunc("/", makeHandler(w))

	fmt.Printf("logit server starting at port '%d'\n", listenPort)
	http.ListenAndServe(fmt.Sprintf(":%d", listenPort), nil)
}
