package main

import (
	"errors"
	wire "github.com/coffeepac/tftp/tftp_wire"
	"net"
	"testing"
	"time"
)

type MockPacketConn struct {
	WriteToBuf     []byte
	ReadFromBuf    [][]byte
	ReadFromErrors []error
	ReadFromAddr   []net.Addr
	Addr           net.Addr
}

func (f *MockPacketConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	b = f.ReadFromBuf[0]
	addr = f.ReadFromAddr[0]
	err = f.ReadFromErrors[0]
	f.ReadFromBuf = f.ReadFromBuf[1:]
	f.ReadFromAddr = f.ReadFromAddr[1:]
	f.ReadFromErrors = f.ReadFromErrors[1:]
	return len(b), nil, nil
}

func (f *MockPacketConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	f.WriteToBuf = b
	return len(b), nil
}

func (f *MockPacketConn) Close() error {
	return nil
}

func (f *MockPacketConn) LocalAddr() net.Addr {
	return f.Addr
}

func (f *MockPacketConn) SetDeadline(t time.Time) error {
	return nil
}

func (f *MockPacketConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (f *MockPacketConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type MockNetError struct {
	err error
}

func (m *MockNetError) Timeout() bool {
	return true
}

func (m *MockNetError) Temporary() bool {
	return true
}

func (m *MockNetError) Error() string {
	return m.err.Error()
}

func TestNewTIDConnection(t *testing.T) {
	// check if txn seed is a. needed b. effective
	conn1 := newTIDConnection(1)
	if conn1 == nil {
		t.Errorf("Unable to allocate ephemeral port for conn1.")
	}
	conn1.Close()

	conn2 := newTIDConnection(1)
	if conn2 == nil {
		t.Errorf("Unable to allocate ephemeral port for conn2.")
	}
	conn2.Close()

	if conn1.LocalAddr().String() != conn2.LocalAddr().String() {
		t.Errorf("Expected to get same net.Addr value if using same seed to newTIDConnection")
	}

	conn3 := newTIDConnection(3)
	if conn3 == nil {
		t.Errorf("Unable to allocate ephemeral port for conn3.")
	}
	conn3.Close()

	if conn1.LocalAddr().String() == conn3.LocalAddr().String() {
		t.Errorf("Expected to get different net.Addr value if using different seed to newTIDConnection")
	}

}

func TestMockPacketConn(t *testing.T) {
	//  expect Mock interface to succeed type assertion to net.PacketConn
	mockConn := MockPacketConn{}
	_ = net.PacketConn(&mockConn)
}

func TestErrorPackets(t *testing.T) {
	mockConn := &MockPacketConn{}
	futureAck(nil, mockConn)
	fAPack, err := wire.ParsePacket(mockConn.WriteToBuf)
	if err != nil {
		t.Errorf("futureAck not creating a parseable packet, error: %s", err)
	}
	faError, ok := fAPack.(*wire.PacketError)
	if !ok {
		t.Errorf("futureAck not creating a valid tftp error packet.")
	} else if faError.Msg != "Received ACK for packet not yet sent." {
		t.Errorf("futureAck not setting return msg properly.")
	}

	unsupportedMode(nil, mockConn)
	unModePack, err := wire.ParsePacket(mockConn.WriteToBuf)
	if err != nil {
		t.Errorf("unsupportedMode not creating a parseable packet, error: %s", err)
	}
	unsupMode, ok := unModePack.(*wire.PacketError)
	if !ok {
		t.Errorf("unsupportedMode not creating a valid tftp error packet.")
	} else if unsupMode.Msg != "This server only supports a mode of OCTET" {
		t.Errorf("unsupportedMode not setting return msg properly.")
	}

	unknownRemoteTID(nil, mockConn)
	unkRemotePack, err := wire.ParsePacket(mockConn.WriteToBuf)
	if err != nil {
		t.Errorf("unknownRemoteTID not creating a parseable packet, error: %s", err)
	}
	unkRemote, ok := unkRemotePack.(*wire.PacketError)
	if !ok {
		t.Errorf("unknownRemoteTID not creating a valid tftp error packet.")
	} else if unkRemote.Msg != "TID is not known to this server" {
		t.Errorf("unknownRemoteTID not setting return msg properly.")
	}

	unexpectedPacket(nil, mockConn, "DATA")
	unExPack, err := wire.ParsePacket(mockConn.WriteToBuf)
	if err != nil {
		t.Errorf("unexpectedPacket not creating a parseable packet, error: %s", err)
	}
	unEx, ok := unExPack.(*wire.PacketError)
	if !ok {
		t.Errorf("unexpectedPacket not creating a valid tftp error packet.")
	} else if unEx.Msg != "Was expecting DATA packet" {
		t.Errorf("unexpectedPacket not setting return msg properly.")
	}

	badPacket(nil, mockConn, errors.New("mock bad packet"))
	badPack, err := wire.ParsePacket(mockConn.WriteToBuf)
	if err != nil {
		t.Errorf("badPacket not creating a parseable packet, error: %s", err)
	}
	bad, ok := badPack.(*wire.PacketError)
	if !ok {
		t.Errorf("badPacket not creating a valid tftp error packet.")
	} else if bad.Msg != "Malformed packet" {
		t.Errorf("badPacket not setting return msg properly.")
	}

}

func TestTftpReadFromOpRead(t *testing.T) { // processing ACKs
	mockConn := &MockPacketConn{ReadFromBuf: make([][]byte, 10), ReadFromAddr: make([]net.Addr, 10), ReadFromErrors: make([]error, 10)}
	ack1 := wire.PacketAck{BlockNum: 0}
	addr1 := &net.UDPAddr{Port: 2000}

	// happy path
	mockConn.ReadFromBuf[0] = ack1.Serialize()
	mockConn.ReadFromAddr[0] = addr1
	mockConn.ReadFromErrors[0] = nil

	data, _, err := tftpReadFrom(mockConn, addr1, nil)
	if err != nil {
		t.Errorf("received error, should have been <nil>")
	} else if data[3] != ack1.Serialize()[3] { //  all single digit BlockNums
		t.Errorf("data corrupted by tftpReadFrom")
	}

	// unknown remote TID
	addr2 := &net.UDPAddr{Port: 2001}

	mockConn.ReadFromBuf[0] = ack1.Serialize()
	mockConn.ReadFromAddr[0] = addr1
	mockConn.ReadFromErrors[0] = nil

	data, _, err = tftpReadFrom(mockConn, addr2, nil)
	if err == nil {
		t.Errorf("did not receive error, should have. remote TID is unknown")
	} else if err.Error() != "Errant packet received" {
		t.Errorf("Received incorrect error message: %s", err)
	}

	// successful retry
	mockConn.ReadFromBuf[0] = ack1.Serialize()
	mockConn.ReadFromAddr[0] = addr1
	mockConn.ReadFromErrors[0] = &MockNetError{err: errors.New("A wild timeout appears")}

	mockConn.ReadFromBuf[1] = ack1.Serialize()
	mockConn.ReadFromAddr[1] = addr1
	mockConn.ReadFromErrors[1] = nil

	dataPack := wire.PacketData{BlockNum: 0, Data: []byte("Murgatroyd")}
	if mockConn.WriteToBuf != nil {
		t.Errorf("WriteToBuf has data.  That's wrong.")
	}
	data, _, err = tftpReadFrom(mockConn, addr1, dataPack.Serialize())
	if err != nil {
		t.Errorf("received error, should not have.  error: %s", err)
	} else if string(mockConn.WriteToBuf[4:13]) != "Murgatroyd" {
		t.Errorf("Failed to resend correct data.  Sent: %s", mockConn.WriteToBuf)
	} else if data[3] != ack1.Serialize()[3] { //  all single digit BlockNums
		t.Errorf("data corrupted by tftpReadFrom")
	}
}
