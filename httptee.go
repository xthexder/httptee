package main

import (
	"bytes"
	"flag"
	"io"
	"net"
	"sort"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/sergi/go-diff/diffmatchpatch"
)

var base, compare string

func PrintDiff(diff []diffmatchpatch.Diff) {
	output := ""
	for _, change := range diff {
		switch change.Type {
		case diffmatchpatch.DiffDelete:
			output += "- " + change.Text
		case diffmatchpatch.DiffInsert:
			output += "+ " + change.Text
		}
	}
	if len(output) > 0 {
		log.Println("Headers differ:\n" + output)
	}
}

func Handle(conn *net.TCPConn) {
	log.Debug("Connection from ", conn.RemoteAddr().String())

	conna, err := net.Dial("tcp", base)
	if err != nil {
		log.Warn("base backend: ", err)
		conn.Close()
		return
	}
	connb, err := net.Dial("tcp", compare)
	if err != nil {
		log.Warn("compare backend: ", err)
	}
	defer conna.Close()
	done := make(chan struct{})

	request := new(bytes.Buffer)

	go func() {
		defer conn.Close()
		buffa := new(bytes.Buffer)
		buffb := new(bytes.Buffer)
		reader := io.TeeReader(conna, buffa)
		go func() {
			if connb == nil {
				return
			}
			defer connb.Close()
			buf := make([]byte, 32*1024)
			for {
				connb.SetReadDeadline(time.Now().Add(1 * time.Second))
				n, err := connb.Read(buf)
				if err != nil {
					break
				}
				buffb.Write(buf[:n])
				if n == 0 { // Just assume we're at the end
					break
				}
			}
			close(done)
		}()
		io.Copy(conn, reader)
		if connb != nil {
			<-done

			splita := append(strings.SplitN(buffa.String(), "\r\n\r\n", 2), "", "")
			splitb := append(strings.SplitN(buffb.String(), "\r\n\r\n", 2), "", "")
			linesa := strings.Split(splita[0], "\r\n")
			linesb := strings.Split(splitb[0], "\r\n")
			if len(linesa) > 0 && len(linesb) > 0 {
				sort.Strings(linesa[1:])
				sort.Strings(linesb[1:])
				respa := strings.Join(linesa[1:], "\r\n") + "\r\n"
				respb := strings.Join(linesb[1:], "\r\n") + "\r\n"
				differ := diffmatchpatch.New()
				charsa, charsb, fulltext := differ.DiffLinesToChars(respa, respb)
				diff := differ.DiffMain(charsa, charsb, false)
				diff = differ.DiffCharsToLines(diff, fulltext)

				log.Println("Request:\n" + request.String())
				if linesa[0] != linesb[0] {
					log.Println("Response code differs:\n- " + linesa[0] + "\n+ " + linesb[0])
				} else {
					log.Println("Response code: " + linesa[0])
				}
				PrintDiff(diff)
				if splita[1] != splitb[1] {
					log.Println("Response body differs")
				}
			} else {
				log.Warn("Response missing")
			}
		}
		log.Debug("Done")
	}()

	if connb != nil {
		reader1 := io.TeeReader(conn, request)
		reader2 := io.TeeReader(reader1, connb)
		io.Copy(conna, reader2)
	} else {
		reader := io.TeeReader(conn, request)
		io.Copy(conna, reader)
	}
}

func main() {
	var addr string
	var verbose bool

	flag.StringVar(&addr, "addr", ":8080", "TCP address to bind")
	flag.StringVar(&base, "base", ":8081", "main upstream host to proxy")
	flag.StringVar(&compare, "compare", ":8082", "secondary upstream host to proxy")
	flag.BoolVar(&verbose, "verbose", false, "verbose logging")
	flag.Parse()

	if verbose {
		log.SetLevel(log.DebugLevel)
	}

	tcpaddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}

	listener, err := net.ListenTCP("tcp", tcpaddr)
	if err != nil {
		panic(err)
	}

	log.Println("Listening on: " + tcpaddr.String())

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Warn("accept: ", err)
			continue
		}
		go Handle(conn)
	}
}
