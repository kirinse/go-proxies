// Copyright 2009 The Go Authors. All rights reserved.
// Changes Copyright 2012, Sudhi Herle <sudhi -at- herle.net>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package logger implements a simple logging package. It defines a type, Logger,
// with methods for formatting output. It also has a predefined 'standard'
// Logger accessible through helper functions Print[f|ln], Fatal[f|ln], and
// Panic[f|ln], which are easier to use than creating a Logger manually.
// That logger writes to standard error and prints the date and time
// of each logged message.
// The Fatal functions call os.Exit(1) after writing the log message.
// The Panic functions call panic after writing the log message.
//
// ---------- Enhancements done by Sudhi Herle ------------
//
// Additional functionality compared to the log package:
//
// - Log levels define a heirarchy; an instance of a logger is
//   configured with a given log level; and it only prints log
//   messages "above" the configured level. e.g., if a logger is
//   configured with level of INFO, then it will print all log
//   messages with INFO and higher priority; in particular, it won't
//   print DEBUG messages.
//
// - A single program can have multiple loggers each with a
//   different priority
//
// - The logger method Backtrace() will print a stack backtrace to
//   the configured output stream. Log levels are NOT
//   considered when backtraces are printed.
//
// - The Panic() and Fatal() logger methods implicitly print the
//   stack backtrace (upto 5 levels).
//
// - DEBUG, ERR, CRIT log outputs (via Debug(), Err() and Crit()
//   methods) also print the source file location from whence they
//   were invoked.
//
// - New package functions to create a syslog(1) or a file logger
//   instance.
//
// - Callers can create a new logger instance if they have an
//   io.writer instance of their own - in case the existing output
//   streams (File and Syslog) are insufficient.

package logger

import (
    "fmt"
    "io"
    "os"
    "runtime"
    "sync"
    "time"
    "log/syslog"
    "errors"
    "strings"
)

// These flags define which text to prefix to each log entry generated by the Logger.
const (
    // Bits or'ed together to control what's printed. There is no control over the
    // order they appear (the order listed here) or the format they present (as
    // described in the comments).  A colon appears after these items:
    //  2009/01/23 01:23:23.123123 /a/b/c/d.go:23: message
    Ldate         = 1 << iota     // the date: 2009/01/23
    Ltime                         // the time: 01:23:23
    Lmicroseconds                 // microsecond resolution: 01:23:23.123123.  assumes Ltime.
    Llongfile                     // full file name and line number: /a/b/c/d.go:23
    Lshortfile                    // final file name element and line number: d.go:23. overrides Llongfile
    Lsyslog                       // set to indicate that output destination is syslog
    LstdFlags     = Ldate | Ltime // initial values for the standard logger
)

// Log priority. These form a heirarchy.
// An instance of a logger is configured with a given log level;
// and it only prints log messages "above" the configured level.
// e.g.,
//    if a logger is configured with level of INFO, then it
//    will print all log messages with INFO and higher priority;
//    in particular, it won't print DEBUG messages.
type Priority int

const (
    LOG_NONE Priority = iota
    LOG_DEBUG
    LOG_INFO
    LOG_WARNING
    LOG_ERR
    LOG_CRIT
    LOG_EMERG
)

// Map string names to actual priority levels. Useful for taking log
// levels defined in config files and turning them into usable
// priorities.
var PrioName = map[string]Priority {
    "LOG_DEBUG": LOG_DEBUG,
    "LOG_INFO":  LOG_INFO,
    "LOG_WARNING": LOG_WARNING,
    "LOG_WARN": LOG_WARNING,
    "LOG_ERR": LOG_ERR,
    "LOG_ERROR": LOG_ERR,
    "LOG_CRIT": LOG_CRIT,
    "LOG_EMERG": LOG_EMERG,

    "DEBUG": LOG_DEBUG,
    "INFO":  LOG_INFO,
    "WARNING": LOG_WARNING,
    "WARN": LOG_WARNING,
    "ERR": LOG_ERR,
    "ERROR": LOG_ERR,
    "CRIT": LOG_CRIT,
    "CRITICAL": LOG_CRIT,
    "EMERG": LOG_EMERG,
    "EMERGENCY": LOG_EMERG,
}


var PrioString = map[Priority]string {
    LOG_DEBUG: "DEBUG",
    LOG_INFO:  "INFO",
    LOG_WARNING: "WARNING",
    LOG_ERR: "ERROR",
    LOG_CRIT: "CRITICAL",
    LOG_EMERG: "EMERGENCY",
}


// A Logger represents an active logging object that generates lines of
// output to an io.Writer.  Each logging operation makes a single call to
// the Writer's Write method.  A Logger can be used simultaneously from
// multiple goroutines; it guarantees to serialize access to the Writer.
type Logger struct {
    mu     sync.Mutex // ensures atomic changes to properties
    prio   Priority   // Logging priority
    prefix string     // prefix to write at beginning of each line
    flag   int        // properties
    out    io.Writer  // destination for output
    logch chan string // for doing async (background) I/O
}



// make a async goroutine for doing actual I/O 
func newLogger(ll *Logger) (*Logger, error) {

    ll.logch = make(chan string)

    // Now fork a go routine to do synchronous log writes
    ff := func(l *Logger) {
        for {
            s := <-l.logch
            l.out.Write([]byte(s))
        }
    }

    go ff(ll)

    return ll, nil
}

// New creates a new Logger.   The out variable sets the
// destination to which log data will be written.
// The prefix appears at the beginning of each generated log line.
// The flag argument defines the logging properties.
func New(out io.Writer, prio Priority, prefix string, flag int) (*Logger, error) {
    return newLogger(&Logger{out: out, prio: prio, prefix: prefix, flag: flag})
}


// Open a new file logger to write logs to 'file'.
// This function erases the previous file contents.
func NewFilelog(file string, prio Priority, prefix string, flag int) (*Logger, error) {

    logfd, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0600)
    if err != nil {
        s := fmt.Sprintf("Can't open log file '%s': %s", file, err)
        return nil, errors.New(s)
    }

    return newLogger(&Logger{out: logfd, prio: prio, prefix: prefix, flag: flag})
}


// Open a new syslog logger
// XXX What happens on Win32?
func NewSyslog(prio Priority, prefix string, flag int) (*Logger, error) {
    wr, err := syslog.New(syslog.LOG_ERR, "")
    if err != nil {
        s := fmt.Sprintf("Can't open SYSLOG connection: %s", err)
        return nil, errors.New(s)
    }

    return newLogger(&Logger{out: wr, prio: prio, prefix: prefix, flag: flag|Lsyslog})
}

// Create a new file logger or syslog logger
func NewLogger(name string, prio Priority, prefix string, flag int) (*Logger, error) {
    if strings.ToUpper(name) == "SYSLOG" {
        return NewSyslog(prio, prefix, flag)
    }

    return NewFilelog(name, prio, "", flag)
}

// Cheap integer to fixed-width decimal ASCII.  Give a negative width to avoid zero-padding.
// Knows the buffer has capacity.
func itoa(i int, wid int) string {
    var u uint = uint(i)
    if u == 0 && wid <= 1 {
        return "0"
    }

    // Assemble decimal in reverse order.
    var b [32]byte
    bp := len(b)
    for ; u > 0 || wid > 0; u /= 10 {
        bp--
        wid--
        b[bp] = byte(u%10) + '0'
    }

    return string(b[bp:])
}

func (l *Logger) formatHeader(t time.Time) string {
    var s string

    //*buf = append(*buf, l.prefix...)
    if l.flag&(Ldate|Ltime|Lmicroseconds) != 0 {
        if l.flag&Ldate != 0 {
            year, month, day := t.Date()
            s += itoa(year, 4)
            s += "/"
            s += itoa(int(month), 2)
            s += "/"
            s += itoa(day, 2)
        }
        if l.flag&(Ltime|Lmicroseconds) != 0 {
            hour, min, sec := t.Clock()

            s += " "
            s += itoa(hour, 2)
            s += ":"
            s += itoa(min, 2)
            s += ":"
            s += itoa(sec, 2)
            if l.flag&Lmicroseconds != 0 {
                s += "."
                s += itoa(t.Nanosecond()/1e3, 6)
            }
        }

        s += " "
    }
    return s
}

// Output writes the output for a logging event.  The string s contains
// the text to print after the prefix specified by the flags of the
// Logger.  A newline is appended if the last character of s is not
// already a newline.  Calldepth is used to recover the PC and is
// provided for generality, although at the moment on all pre-defined
// paths it will be 2.
func (l *Logger) Output(calldepth int, prio Priority, s string) error {
    if len(s) == 0 {
        return nil
    }

    var buf string

    // Put the timestamp and priority only if we are NOT syslog
    if (l.flag & Lsyslog) == 0 {
        now := time.Now()
        buf  = fmt.Sprintf("<%d>:%s", prio, l.formatHeader(now))
    }

    if calldepth > 0 {
        var file string
        var line int
        var finfo string
        if l.flag&(Lshortfile|Llongfile) != 0 {
            var ok bool
            _, file, line, ok = runtime.Caller(calldepth)
            if !ok {
                file = "???"
                line = 0
            }
            if l.flag&Lshortfile != 0 {
                short := file
                for i := len(file) - 1; i > 0; i-- {
                    if file[i] == '/' {
                        short = file[i+1:]
                        break
                    }
                }
                file = short
            }
            finfo = fmt.Sprintf("(%s:%d) ", file, line)
        }

        if len(finfo) > 0 {
            buf += finfo
        }
    }


    //buf = append(buf, fmt.Sprintf(":<%d>: ", prio)...)
    buf += s
    if s[len(s)-1] != '\n' {
        buf += "\n"
    }

    l.logch <- buf
    return nil
}

// Dump stack backtrace for 'depth' levels
// Backtrace is of the form "file:line [func name]"
// NB: The absolute pathname of the file is used in the backtrace -
//     regardless of the logger flags requesting shortfile.
func (l* Logger) Backtrace(depth int) {
    var pc []uintptr = make([]uintptr, 64)
    var v  []string

    // runtime.Callers() requires a pre-created array.
    n := runtime.Callers(3, pc)

    if depth == 0 || depth > n {
        depth = n
    } else if n > depth {
        n = depth
    }

    for i := 0; i < n; i++ {
        var s string = "*unknown*"
        p := pc[i]
        f := runtime.FuncForPC(p)

        if f != nil {
            nm := f.Name()
            file, line := f.FileLine(p)
            s = fmt.Sprintf("%s:%d [%s]", file, line, nm)
        }
        v = append(v, s)
    }
    v = append(v, "\n")

    str := "Backtrace:\n    " + strings.Join(v, "\n    ")
    l.logch <- str
}


// Predicate that returns true if we can log at level prio
func (l* Logger) Loggable(prio Priority) bool {
    return l.prio >= LOG_NONE && prio  >= l.prio
}




// Printf calls l.Output to print to the logger.
// Arguments are handled in the manner of fmt.Printf.
func (l *Logger) Printf(format string, v ...interface{}) {
    l.Output(0, LOG_INFO, fmt.Sprintf(format, v...))
}

// Print calls l.Output to print to the logger.
// Arguments are handled in the manner of fmt.Print.
func (l *Logger) Print(v ...interface{}) { l.Output(0, LOG_INFO, fmt.Sprint(v...)) }


// Fatalf is equivalent to l.Printf() followed by a call to os.Exit(1).
func (l *Logger) Fatal(format string, v ...interface{}) {
    l.Output(2, LOG_EMERG, fmt.Sprintf(format, v...))
    l.Backtrace(0)
    os.Exit(1)
}


// Panicf is equivalent to l.Printf() followed by a call to panic().
func (l *Logger) Panic(format string, v ...interface{}) {
    s := fmt.Sprintf(format, v...)
    l.Output(2, LOG_EMERG, s)
    l.Backtrace(5)
    panic(s)
}



// Crit prints logs at level CRIT
func (l *Logger) Crit(format string, v ...interface{}) {
    if l.Loggable(LOG_CRIT) {
        s := fmt.Sprintf(format, v...)
        l.Output(2, LOG_CRIT, s)
    }
}


// Err prints logs at level ERR
eunc (l *Logger) Error(format string, v ...interface{}) {
    if l.Loggable(LOG_ERR) {
        s := fmt.Sprintf(format, v...)
        l.Output(2, LOG_ERR, s)
    }
}

// Warn prints logs at level WARNING
func (l *Logger) Warn(format string, v ...interface{}) {
    if l.Loggable(LOG_WARNING) {
        s := fmt.Sprintf(format, v...)
        l.Output(0, LOG_WARNING, s)
    }
}


// Info prints logs at level INFO
func (l *Logger) Info(format string, v ...interface{}) {
    if l.Loggable(LOG_INFO) {
        s := fmt.Sprintf(format, v...)
        l.Output(0, LOG_INFO, s)
    }
}


// Debug prints logs at level INFO
func (l *Logger) Debug(format string, v ...interface{}) {
    if l.Loggable(LOG_DEBUG) {
        s := fmt.Sprintf(format, v...)
        l.Output(2, LOG_DEBUG, s)
    }
}


// Manipulate properties of loggers


// Return priority of this logger
func (l* Logger) Prio() Priority {
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.prio
}

// Set priority
func (l* Logger) SetPrio(prio Priority) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.prio = prio
}

// Flags returns the output flags for the logger.
func (l *Logger) Flags() int {
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.flag
}

// SetFlags sets the output flags for the logger.
func (l *Logger) SetFlags(flag int) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.flag = flag
}

// Prefix returns the output prefix for the logger.
func (l *Logger) Prefix() string {
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.prefix
}

// SetPrefix sets the output prefix for the logger.
func (l *Logger) SetPrefix(prefix string) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.prefix = prefix
}

// --- EOF ---

