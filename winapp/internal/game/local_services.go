package game

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
)

type LocalLauncherServicesConfig struct {
	Server  ServerConfig
	Account AccountState
}

type LocalLauncherServices struct {
	rpc  *localGameRPCService
	auth *localAuthService
	done chan struct{}
	once sync.Once
}

func StartLocalLauncherServices(ctx context.Context, config LocalLauncherServicesConfig) (*LocalLauncherServices, error) {
	rpc, err := startLocalGameRPCService(config.Server)
	if err != nil {
		return nil, err
	}
	auth, err := startLocalAuthService(config)
	if err != nil {
		rpc.Close()
		return nil, err
	}
	services := &LocalLauncherServices{rpc: rpc, auth: auth, done: make(chan struct{})}
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				services.Close()
			case <-services.done:
			}
		}()
	}
	return services, nil
}

func (s *LocalLauncherServices) RPCPort() int {
	if s == nil || s.rpc == nil {
		return 0
	}
	return s.rpc.port
}

func (s *LocalLauncherServices) AuthPort() int {
	if s == nil || s.auth == nil {
		return 0
	}
	return s.auth.port
}

func (s *LocalLauncherServices) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.done != nil {
			close(s.done)
		}
		if s.rpc != nil {
			s.rpc.Close()
		}
		if s.auth != nil {
			s.auth.Close()
		}
	})
}

type localGameRPCService struct {
	listener net.Listener
	port     int
	done     chan struct{}
	once     sync.Once
	server   ServerConfig
}

func startLocalGameRPCService(server ServerConfig) (*localGameRPCService, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	service := &localGameRPCService{listener: listener, port: listener.Addr().(*net.TCPAddr).Port, done: make(chan struct{}), server: server}
	go service.acceptLoop()
	return service, nil
}

func (s *localGameRPCService) Close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.listener.Close()
	})
}

func (s *localGameRPCService) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *localGameRPCService) handleConn(conn net.Conn) {
	defer conn.Close()
	for {
		message, err := readRPCFrame(conn)
		if err != nil {
			return
		}
		if len(message) < 2 {
			continue
		}
		opcode := binary.LittleEndian.Uint16(message[:2])
		response, ok := s.handleMessage(opcode, message[2:])
		if !ok {
			continue
		}
		if err := writeRPCFrame(conn, response); err != nil {
			return
		}
	}
}

func (s *localGameRPCService) handleMessage(opcode uint16, payload []byte) ([]byte, bool) {
	switch opcode {
	case 18:
		return append(simplePack(uint16(18)), payload...), true
	case 261:
		if s.server.Version > Version1_18 {
			return simplePack(uint16(1799), s.server.IP, int32(s.server.Port), s.server.Username), true
		}
	case 512:
		return simplePack(uint16(512), "i'am wpflauncher"), true
	case 517:
		return simplePack(uint16(1031), s.server.IP, int32(s.server.Port), s.server.Username, false), true
	case 1298:
		return simplePack(uint16(1298), false, int64(0), int64(0)), true
	}
	return nil, false
}

func readRPCFrame(reader io.Reader) ([]byte, error) {
	var size uint16
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, errors.New("empty rpc frame")
	}
	message := make([]byte, int(size))
	_, err := io.ReadFull(reader, message)
	return message, err
}

func writeRPCFrame(writer io.Writer, message []byte) error {
	if len(message) > 0xffff {
		return errors.New("rpc frame too large")
	}
	if err := binary.Write(writer, binary.LittleEndian, uint16(len(message))); err != nil {
		return err
	}
	_, err := writer.Write(message)
	return err
}

func simplePack(values ...any) []byte {
	buffer := make([]byte, 0, 64)
	for _, value := range values {
		switch v := value.(type) {
		case bool:
			if v {
				buffer = append(buffer, 1)
			} else {
				buffer = append(buffer, 0)
			}
		case byte:
			buffer = append(buffer, v)
		case int16:
			buffer = binary.LittleEndian.AppendUint16(buffer, uint16(v))
		case uint16:
			buffer = binary.LittleEndian.AppendUint16(buffer, v)
		case int32:
			buffer = binary.LittleEndian.AppendUint32(buffer, uint32(v))
		case uint32:
			buffer = binary.LittleEndian.AppendUint32(buffer, v)
		case int64:
			buffer = binary.LittleEndian.AppendUint64(buffer, uint64(v))
		case uint64:
			buffer = binary.LittleEndian.AppendUint64(buffer, v)
		case int:
			buffer = binary.LittleEndian.AppendUint32(buffer, uint32(v))
		case string:
			bytes := []byte(v)
			buffer = binary.LittleEndian.AppendUint16(buffer, uint16(len(bytes)))
			buffer = append(buffer, bytes...)
		case []byte:
			buffer = append(buffer, v...)
		}
	}
	return buffer
}

type localAuthService struct {
	listener net.Listener
	port     int
	done     chan struct{}
	once     sync.Once
	config   LocalLauncherServicesConfig
}

func startLocalAuthService(config LocalLauncherServicesConfig) (*localAuthService, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	service := &localAuthService{listener: listener, port: listener.Addr().(*net.TCPAddr).Port, done: make(chan struct{}), config: config}
	go service.acceptLoop()
	return service, nil
}

func (s *localAuthService) Close() {
	s.once.Do(func() {
		close(s.done)
		_ = s.listener.Close()
	})
}

func (s *localAuthService) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *localAuthService) handleConn(conn net.Conn) {
	defer conn.Close()
	_, _ = readAuthString(conn)
	_, _ = readAuthString(conn)
	_, _ = readAuthString(conn)
	_ = binary.Write(conn, binary.LittleEndian, uint32(0))
}

func readAuthString(reader io.Reader) (string, error) {
	var size int32
	if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
		return "", err
	}
	if size < 0 || size > 64*1024 {
		return "", errors.New("invalid auth string size")
	}
	data := make([]byte, int(size))
	_, err := io.ReadFull(reader, data)
	return string(data), err
}
