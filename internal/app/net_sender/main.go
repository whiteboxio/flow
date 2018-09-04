package main

import (
	"bytes"
	"flag"
	"io/ioutil"
	"net"
	"os"
	"regexp"
	"time"

	log "github.com/sirupsen/logrus"
)

func main() {
	srcFile := flag.String("source", "", "Data source file")
	sendTo := flag.String("send-to", "", "Receiver net address")
	num := flag.Uint64("n", 0, "Send up to N messages (0 for no limit)")
	proto := flag.String("proto", "tcp", "Network protocol (tcp, udp, unix)")

	flag.Parse()

	log.Infof("Reading data from %s", *srcFile)
	data, err := ioutil.ReadFile(*srcFile)
	if err != nil {
		log.Fatalf("Failed to read data from source file: %s", err)
	}
	connExpResp, err := regexp.Match("^tcp*", []byte(*proto))
	if err != nil {
		log.Errorf("Unexpected error: %s", err)
	}

	log.Infof("Creating a new connection to %s", *sendTo)
	conn, err := net.Dial(*proto, *sendTo)
	if err != nil {
		log.Fatalf("Failed to open a connection to %s: %s", *sendTo, err)
	}

	dataMsgs := bytes.Split(data, []byte{'\n'})

	var sentCnt uint64
	var failCnt uint64
	msgIx := 0
	msgCnt := len(dataMsgs)
	respBuf := make([]byte, 1024)
	if connExpResp {
		log.Infof("Expected response from connection")
	} else {
		log.Infof("A response from connection is not expected")
	}
	for {
		if *num > 0 {
			if sentCnt >= *num {
				break
			}
		}
		conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err := conn.Write(append(dataMsgs[msgIx], '\r', '\n')); err != nil {
			log.Errorf("Failed to send data [%s]: %s", dataMsgs[msgIx], err)
			failCnt++
			continue
		} else {
			if connExpResp {
				n, err := conn.Read(respBuf)
				if err != nil {
					log.Errorf("Failed to read data: %s", err)
					failCnt++
				}
				if n == 0 {
					log.Errorf("No response received from the server")
					failCnt++
				}
			}
		}
		sentCnt++
		msgIx++
		msgIx = msgIx % msgCnt
	}

	log.Infof("Sent %d, Failed: %d", sentCnt, failCnt)

	os.Exit(0)
}