package main

import (
	"errors"
	"fmt"
	wire "github.com/coffeepac/tftp/tftp_wire"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var files map[string]string
var connectionAttempts = 15                                                            // attempts to randomly find an unused port
var connRetries = 5                                                                    // attempts to send or wait
var portRangeStart = 49152                                                             // IANA recommended port range start for ephemeral ports
var portRangeSize = 16383                                                              // IANA recommended port range size for ephemeral ports
var timeoutSeconds = 10                                                                // timeout for all ReadFroms in seconds
var txnTemplate = "Transaction #%d of type %s completed with status %s and notes %s\n" // template so all txn log messages look the same

func futureAck(addr net.Addr, conn net.PacketConn) {
	errPack := wire.PacketError{Code: uint16(0), Msg: "Received ACK for packet not yet sent."}
	conn.WriteTo(errPack.Serialize(), addr)
	log.Println("Received an ACK for a packet not yet sent.  Aborting connection.")
}

func unexpectedPacket(addr net.Addr, conn net.PacketConn, packetType string) {
	errPack := wire.PacketError{Code: uint16(0), Msg: "Was expecting " + packetType + " packet"}
	conn.WriteTo(errPack.Serialize(), addr)
	log.Println("Received an unexpected packet type, wasn't " + packetType + ".  Aborting connection.")
}

func badPacket(addr net.Addr, conn net.PacketConn, err error) {
	badPacket := wire.PacketError{Code: 0, Msg: "Malfomred packet"}
	conn.WriteTo(badPacket.Serialize(), addr)
	log.Println("Received an incorrectly formatted packet.  Aborting connection.  error: ", err)
}

func unsupportedMode(addr net.Addr, conn net.PacketConn) {
	unsupModePacket := wire.PacketError{Code: 0, Msg: "This server only supports a mode of OCTET"}
	conn.WriteTo(unsupModePacket.Serialize(), addr)
	log.Println("Received a mode other then OCTET.  Aborting connection.")
}

func unknownRemoteTID(addr net.Addr, conn net.PacketConn) {
	unknownTID := wire.PacketError{Code: 0, Msg: "TID is not known to this server"}
	conn.WriteTo(unknownTID.Serialize(), addr)
	log.Println("Received a packet from an unknown TID.")
}

func newTIDConnection(seed int64) net.PacketConn {
	rand.Seed(seed)
	for attempts := connectionAttempts; attempts > 0; attempts-- {
		port := portRangeStart + rand.Intn(portRangeSize) // IANA recommended ephemeral port range of 49512 - 65535
		connection, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
		if err != nil {
			log.Printf("Unable to bind to port %d.  %d attempts left", port, attempts)
		} else {
			return connection
		}
	}

	log.Println("Unable to select an ephemeral port at random.  Return no connection")
	return nil
}

func tftpReadFrom(conn net.PacketConn, addr net.Addr, prevData []byte) ([]byte, int, error) {
	retryCounter := 0
	readComplete := false
	data := make([]byte, 516)
	n := 0
	var readAddr net.Addr
	var err error
	for retryCounter < connRetries && !readComplete {
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		n, readAddr, err = conn.ReadFrom(data)
		if err != nil && err.(net.Error).Timeout() == true {
			conn.WriteTo(prevData, addr)
		} else if err != nil {
			return data, n, err // general errors end this loop, don't bother resetting deadline.  conn will be closed before used again
		} else {
			readComplete = true
		}
		retryCounter++
	}

	if readComplete {
		if readAddr.String() != addr.String() {
			unknownRemoteTID(readAddr, conn)
			conn.SetDeadline(time.Time{}) // reset to infinity
			return nil, 0, errors.New("Errant packet received")
		}
		conn.SetDeadline(time.Time{}) // reset to infinity
		return data, n, nil
	} else {
		return data, n, errors.New("ReadFrom timed out")
	}
}

func opRead(request *wire.PacketRequest, addr net.Addr, txID int64, txns chan string) {
	conn := newTIDConnection(txID)
	if conn == nil {
		txns <- fmt.Sprintf(txnTemplate, txID, "READ", "failed", "unable to open new TID connection")
		return
	}
	defer conn.Close()

	if fileContents, ok := files[request.Filename]; ok {
		notDone := true
		blockNum := uint16(1)
		for notDone {
			var chunk []byte
			if len(fileContents) >= 512 {
				chunk = []byte(fileContents[:512])
				fileContents = fileContents[512:]
			} else {
				chunk = []byte(fileContents)
				notDone = false
			}
			data := wire.PacketData{BlockNum: blockNum, Data: chunk}
			conn.WriteTo(data.Serialize(), addr)
			ackReceived := false
			for !ackReceived {
				buf, _, err := tftpReadFrom(conn, addr, data.Serialize())
				if err != nil {
					if err.Error() == "Errant packet received" {
						continue
					} else {
						log.Println("ReadFrom failed.  Aborting. error: ", err)
						txns <- fmt.Sprintf(txnTemplate, txID, "READ", "failed", "ACK packet read failed.  Check application log")
						return
					}
				}
				ackPack, err := wire.ParsePacket(buf)
				if err != nil {
					badPacket(addr, conn, err)
					txns <- fmt.Sprintf(txnTemplate, txID, "READ", "failed", "ACK packet parsing failed.  Check application log")
					return
				}
				ack, ok := ackPack.(*wire.PacketAck)
				if !ok {
					unexpectedPacket(addr, conn, "ACK")
					txns <- fmt.Sprintf(txnTemplate, txID, "READ", "failed", "Received unexpected packet type.  Check application log")
					return
				}
				if ack.BlockNum != blockNum {
					if ack.BlockNum < blockNum {
						continue // probably a retransmit of an old ack
					} else {
						// ACK from the future.  I assume something is Wrong on the sending side.
						futureAck(addr, conn)
						txns <- fmt.Sprintf(txnTemplate, txID, "READ", "failed", "Recevied ACK from future.  Check application log")
						return
					}
				} else {
					ackReceived = true
				}

			}
			blockNum++
		}
	} else {
		errPack := wire.PacketError{Code: 1, Msg: "File not found"}
		conn.WriteTo(errPack.Serialize(), addr)
		txns <- fmt.Sprintf(txnTemplate, txID, "READ", "failed", "Requested file not found.")
		return
	}

	txns <- fmt.Sprintf(txnTemplate, txID, "READ", "success", "<none>")

}

func opWrite(request *wire.PacketRequest, addr net.Addr, txID int64, txns chan string) {
	conn := newTIDConnection(txID)
	if conn == nil {
		txns <- fmt.Sprintf(txnTemplate, txID, "WRITE", "failed", "unable to open new TID connection")
		return
	}
	defer conn.Close()

	// ack the WRQ
	ack := wire.PacketAck{BlockNum: 0}
	_, err := conn.WriteTo(ack.Serialize(), addr)
	if err != nil {
		log.Println("Initial ACK failed.  Aborting. error: ", err)
		txns <- fmt.Sprintf(txnTemplate, txID, "WRITE", "failed", "initial ACK failed")
		return
	}

	// loop over new connection waiting for new packets
	notDone := true
	fileContents := ""
	for notDone {
		buf, n, err := tftpReadFrom(conn, addr, ack.Serialize())
		if err != nil {
			if err.Error() == "Errant packet received" {
				continue
			} else {
				log.Println("ReadFrom failed.  Aborting. error: ", err)
				txns <- fmt.Sprintf(txnTemplate, txID, "WRITE", "failed", "DATA packet read failed.  Check application log")
				return
			}
		} else {
			dPacket, err := wire.ParsePacket(buf)
			if err != nil {
				badPacket(addr, conn, err)
				txns <- fmt.Sprintf(txnTemplate, txID, "WRITE", "failed", "DATA packet parsing failed.  Check application log")
				return
			}
			data, ok := dPacket.(*wire.PacketData)
			if !ok {
				unexpectedPacket(addr, conn, "DATA")
				txns <- fmt.Sprintf(txnTemplate, txID, "WRITE", "failed", "Received unexpected packet type.  Check application log")
				return
			}
			fileContents = fileContents + string(data.Data[:n-4]) // fudge factor for header
			ack = wire.PacketAck{BlockNum: data.BlockNum}
			conn.WriteTo(ack.Serialize(), addr)
			if n < 512 {
				notDone = false
			}
		}
	}
	files[request.Filename] = fileContents
	txns <- fmt.Sprintf(txnTemplate, txID, "WRITE", "success", "<none>")
}

func logTxns(txnFile *os.File, txns chan string) {
	for {
		txn := <-txns
		_, err := txnFile.WriteString(txn)
		if err != nil {
			log.Printf("Failed to log txn '%s' with error '%s'\n", txn, err)
		}
	}
}

func initABit() {
	files = make(map[string]string, 1000)
	files["cheese"] = "This is not the sound of the train"
}

func main() {
	initABit()

	// create server
	server, err := net.ListenPacket("udp", ":9010") //  change to 69 before submit
	if err != nil {
		fmt.Println(err)
	} else {
		defer server.Close()

		// signal handling
		signals := make(chan os.Signal)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

		keepLooping := true
		go func() {
			<-signals
			server.SetDeadline(time.Now())
			keepLooping = false
		}()

		// txn log create
		ex, err := os.Executable()
		if err != nil {
			log.Fatal("Unable to find path to running executable.  Unable to create txn file in sensible location. error: ", err)
		}
		exPath := filepath.Dir(ex)
		txnLog := filepath.Join(exPath, "tftpTxn.log")
		txnFile, err := os.Create(txnLog)
		if err != nil {
			log.Fatal("Unable to create file in same dir as running executable.  Quit.  error: ", err)
		}
		defer txnFile.Close()

		txns := make(chan string)
		go logTxns(txnFile, txns)

		buf := make([]byte, 2048)
		txID := int64(0)
		for keepLooping {
			_, addr, err := server.ReadFrom(buf)
			if err != nil {
				log.Println("Unable to read packet from connection.  Error: ", err)
				txns <- fmt.Sprintf(txnTemplate, txID, "unknown", "failed", "Initial packet unreadable")
			} else {
				packet, err := wire.ParsePacket(buf)
				if err != nil {
					// incorrectly formated packet
					go badPacket(addr, server, err)
					txns <- fmt.Sprintf(txnTemplate, txID, "unknown", "failed", "Initial packet corrupted")
				} else {
					packetRequest, ok := packet.(*wire.PacketRequest)
					if !ok {
						log.Println(packet)
						unexpectedPacket(addr, server, "RRQ or WRQ")
						txns <- fmt.Sprintf(txnTemplate, txID, "unknown", "failed", "Initial packet not RRQ or WRQ")
					} else if strings.ToLower(packetRequest.Mode) != "octet" {
						unsupportedMode(addr, server)
						txns <- fmt.Sprintf(txnTemplate, txID, "unknown", "failed", "Communication not in OCTET mode")
					} else if packetRequest.Op == wire.OpRRQ {
						go opRead(packetRequest, addr, txID, txns)
					} else if packetRequest.Op == wire.OpWRQ {
						go opWrite(packetRequest, addr, txID, txns)
					}
				}
			}
			txID++
		}
	}

	fmt.Println("Full list of files in memory at server quit:")
	for k, _ := range files {
		fmt.Println("filename: ", k)
	}

}
