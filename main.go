package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type (
	Server struct {
		ln         net.Listener
		con        net.Conn
		serverMap  map[string]Server
		done       chan os.Signal
		msgch      chan Message
		Addr       string
		Registered bool
		IsApex     bool
	}
	MessageType int
	Message     struct {
		con        net.Conn
		ServerData Server
		Payload    []byte
		MsgType    MessageType
	}
)

const (
	AnyMsg MessageType = iota
	ServerRegisterMsg
)

func NewServer(addr string, apex bool) *Server {
	s := &Server{
		Addr:      addr,
		IsApex:    apex,
		serverMap: make(map[string]Server),
		msgch:     make(chan Message),
		done:      make(chan os.Signal),
	}

	return s
}

const (
	AuxAddr  = "127.0.0.1:"
	ApexAddr = "127.0.0.1:3000"
)

func (s *Server) Start() {
	var err error
	s.ln, err = net.Listen("tcp", s.Addr)
	if err != nil {
		slog.Error("could not start the server", "err", err)
	}

	go s.accept()
	go s.handleMessages()

	slog.Info("Starting the server on", "addr", s.Addr, "err", err)

	if !s.IsApex {
		fmt.Println("Dialing")
		go s.dialServer(ApexAddr)
	}

	signal.Notify(s.done, os.Interrupt, syscall.SIGABRT, syscall.SIGTERM)
	<-s.done
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err = s.Shutdown(ctx); err != nil {
		slog.Error("could not stop the server", "err", err)
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	temp := make(chan struct{})

	go func() {
		slog.Info("Shutting down the server")
		if s.ln != nil {
			s.ln.Close()
		}
		close(temp)
	}()

	select {
	case <-temp:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) accept() {
	for {
		con, err := s.ln.Accept()
		if err != nil {
			break
		}
		go s.handleConnection(con)
	}
}

func (s *Server) pingPong() {
	tempMessage := Message{
		MsgType: AnyMsg,
		con:     s.con,
		Payload: []byte("ping\n"),
	}

	jsonData, _ := json.Marshal(tempMessage)
	for _, server := range s.serverMap {
		server.con.Write(jsonData)
	}
}

func (s *Server) handleConnection(con net.Conn) {
	sAddr := con.RemoteAddr().String()
	slog.Info("Handling Connection", "ADDR", sAddr)
	if _, found := s.serverMap[sAddr]; !found {
		s.serverMap[sAddr] = Server{
			con: con,
		}
		// go s.dialServer(sAddr)
	}
	buf := make([]byte, 2048)
	defer func() {
		if con != nil {
			con.Close()
			delete(s.serverMap, sAddr)
		}
		slog.Info("Node disconnected", "addr", sAddr)
	}()
	for {
		n, err := con.Read(buf)
		if err != nil {
			break
		}

		s.msgch <- Message{
			con:     con,
			Payload: buf[:n],
		}
	}
}

func (s *Server) handleMessages() {
	var temMsg Message
	for msg := range s.msgch {
		if err := json.Unmarshal(msg.Payload, &temMsg); err != nil {
			slog.Error("could not unmarshal the recieved payload", "err", err)
		}
		switch temMsg.MsgType {
		case AnyMsg:
			fmt.Printf(">>[ANY MESSAGE] %s:\n\t%s\n", msg.con.RemoteAddr().String(), string(msg.Payload))
		case ServerRegisterMsg:
			newServer := temMsg.ServerData
			newServer.con = msg.con
			newServer.Addr = msg.con.RemoteAddr().String()
			s.serverMap[newServer.Addr] = newServer
			fmt.Printf(">>[ADDED] %s:\n\t%+v\n", msg.con.RemoteAddr().String(), newServer)

		}
	}
}

func (s *Server) dialServer(addr string) {
	con, err := net.Dial("tcp", addr)
	if err != nil {
		slog.Error("could not dial into the server", "FROM", s.Addr, "TO", addr)
	}

	sAddr := con.RemoteAddr().String()
	fmt.Println("trying to dial to", sAddr)
	if _, found := s.serverMap[sAddr]; found {
		fmt.Println("already found heree", sAddr)
		return
	} else {
		s.serverMap[sAddr] = Server{Addr: sAddr, con: con}
	}

	msg := Message{
		con:        con,
		Payload:    []byte{},
		MsgType:    ServerRegisterMsg,
		ServerData: *s,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		slog.Error("could not marshal the server struct", "err", err)
	}
	con.Write(jsonData)
	s.handleConnection(con)
}

func main() {
	apexFlag := flag.Bool("apex", false, "--apex | --apex=true | --apex=false [default false]")
	pingFlag := flag.Bool("ping", false, "--ping | --ping=true | --ping=false [default false]")
	flag.Parse()

	var server *Server
	if *apexFlag {
		server = NewServer(ApexAddr, true)
	} else {
		server = NewServer(AuxAddr, false)
	}

	if *pingFlag {
		go func() {
			for {
				time.Sleep(time.Second)
				server.pingPong()
			}
		}()
	}

	server.Start()
}
