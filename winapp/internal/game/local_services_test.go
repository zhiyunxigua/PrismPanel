package game

import (
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestLocalGameRPCHeartbeat(t *testing.T) {
	service, err := startLocalGameRPCService(ServerConfig{Version: Version1_21_8, IP: "127.0.0.1", Port: 25565, Username: "Steve"})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(service.port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := writeRPCFrame(conn, simplePack(uint16(512))); err != nil {
		t.Fatal(err)
	}
	response, err := readRPCFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(response) < 4 || binary.LittleEndian.Uint16(response[:2]) != 512 {
		t.Fatalf("unexpected heartbeat response: %v", response)
	}
}

func TestLocalAuthServiceReturnsSuccess(t *testing.T) {
	service, err := startLocalAuthService(LocalLauncherServicesConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(service.port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	writeAuthString(t, conn, "0")
	writeAuthString(t, conn, "123456")
	writeAuthString(t, conn, "server-id")
	var code uint32
	if err := binary.Read(conn, binary.LittleEndian, &code); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("auth code mismatch: %d", code)
	}
}

func writeAuthString(t *testing.T, conn net.Conn, value string) {
	t.Helper()
	data := []byte(value)
	if err := binary.Write(conn, binary.LittleEndian, int32(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
