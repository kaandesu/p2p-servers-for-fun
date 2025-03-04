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
		ServerMap  map[string]Server
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
	UpdateServerListMsg
)

func NewServer(addr string, apex bool) *Server {
	s := &Server{
		Addr:      addr,
		IsApex:    apex,
		ServerMap: make(map[string]Server),
		msgch:     make(chan Message),
		done:      make(chan os.Signal),
	}

	return s
}

const (
	ApexAddr = "0.0.0.0:3000"
)

func (s *Server) Start() {
	var err error
	s.ln, err = net.Listen("tcp", s.Addr)
	if err != nil {
		slog.Error("could not start the server", "err", err)
	}

	slog.Info("Starting the server on", "addr", s.Addr, "err", err)

	go s.accept()
	go s.handleMessages()

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
	slog.Info("[sending ping]", "from", s.Addr)
	tempMessage := Message{
		MsgType: AnyMsg,
		con:     s.con,
		Payload: []byte("ping\n"),
	}

	jsonData, _ := json.Marshal(tempMessage)
	for _, server := range s.ServerMap {
		server.con.Write(jsonData)
	}
}

func (s *Server) sendServerListTo(con net.Conn) {
	if con == nil || !s.IsApex {
		return
	}

	var serverList []string
	for addr := range s.ServerMap {
		serverList = append(serverList, addr)
	}

	payload, err := json.Marshal(serverList)
	if err != nil {
		slog.Error("[sendServerListTo] Marshal error", "err", err)
		return
	}

	newMsg := &Message{
		con:     s.con,
		MsgType: UpdateServerListMsg,
		Payload: payload,
	}

	jsonData, err := json.Marshal(newMsg)
	if err != nil {
		slog.Error("[sendServerListTo] Marshal error", "err", err)
		return
	}

	con.Write(jsonData)
}

func (s *Server) updateServerList(serverMap []string) {
	s.ServerMap = map[string]Server{}
	for _, addr := range serverMap {
		fmt.Println("adding the addr", addr)
		s.ServerMap[addr] = Server{Addr: addr}
	}
}

func (s *Server) handleConnection(con net.Conn) {
	sAddr := con.RemoteAddr().String()
	slog.Info("Handling Connection", "ADDR", sAddr)
	if _, found := s.ServerMap[sAddr]; !found {
		s.ServerMap[sAddr] = Server{
			con:  con,
			Addr: con.RemoteAddr().String(),
		}

		if s.IsApex {
			fmt.Println("registered addresses")
			for adr, server := range s.ServerMap {
				go s.sendServerListTo(server.con)
				fmt.Println(adr)
			}
			fmt.Println("-------------")
		}
	}
	buf := make([]byte, 2048)
	defer func() {
		slog.Info("Node disconnected", "addr", sAddr)
		slog.Info("Server list prev len", "len", len(s.ServerMap))
		delete(s.ServerMap, sAddr)
		slog.Info("Server list after len", "len", len(s.ServerMap))
		if s.IsApex {
			slog.Info("Sending server lists to other nodes", "addr", sAddr)
			for _, server := range s.ServerMap {
				s.sendServerListTo(server.con)
				fmt.Println("-------registered addresses-------")
				for adr := range s.ServerMap {
					fmt.Println(adr)
				}
				fmt.Println("-------------")
			}

		}
		if con != nil {
			con.Close()
		}
	}()
	for {
		n, err := con.Read(buf)
		if err != nil {
			break
		}

		s.msgch <- Message{
			MsgType: AnyMsg,
			con:     con,
			Payload: buf[:n],
		}
	}
}

func (s *Server) handleMessages() {
	var temMsg Message
	for msg := range s.msgch {
		if msg.Payload != nil {
			if err := json.Unmarshal(msg.Payload, &temMsg); err != nil {
				slog.Error("could not unmarshal the recieved payload", "err", err, "msg", string(msg.Payload))
			}
		}
		switch temMsg.MsgType {
		case AnyMsg:
			fmt.Printf(">>[Discarding msg]:\n\t %s", msg.con.RemoteAddr().String())
		case ServerRegisterMsg:
			newServer := temMsg.ServerData
			newServer.con = msg.con
			newServer.Addr = msg.con.RemoteAddr().String()
			s.ServerMap[newServer.Addr] = newServer
			fmt.Printf(">>[New Server Registered]:\n\t %s\n", msg.con.RemoteAddr().String())
		case UpdateServerListMsg:
			fmt.Println("before", len(s.ServerMap))

			newServer := temMsg.ServerData
			var serverList []string
			if err := json.Unmarshal(temMsg.Payload, &serverList); err != nil {
				slog.Error("could not unmarshal server list payload", "err", err)
				break
			}
			s.updateServerList(serverList)
			slog.Info("[Updated Server List]", "from", msg.con.RemoteAddr().String())
			fmt.Printf(">>[GotUpdateList] %s:\n\tlen:%d\n", msg.con.RemoteAddr().String(), len(newServer.ServerMap))
			fmt.Println("after", len(s.ServerMap))
			for adr := range s.ServerMap {
				fmt.Println(adr)
			}

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
	if _, found := s.ServerMap[sAddr]; found {
		fmt.Println("already found heree", sAddr)
		return
	} else {
		s.ServerMap[sAddr] = Server{Addr: sAddr, con: con}
	}

	msg := Message{
		con:        con,
		MsgType:    ServerRegisterMsg,
		ServerData: *s,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		slog.Error("could not marshal the server struct", "err", err)
	}
	con.Write(append(jsonData, '\n'))
	s.handleConnection(con)
}

func (s *Server) connectOtherNodes() {
	for addr := range s.ServerMap {
		go s.dialServer(addr)
	}
}

func main() {
	apexFlag := flag.Bool("apex", false, "--apex | --apex=true | --apex=false [default false]")
	pingFlag := flag.Bool("ping", false, "--ping | --ping=true | --ping=false [default false]")
	addrFlag := flag.String("addr", "127.0.0.1:3001", "--addr=127.0.0.1:3001  [default 127.0.0.1:]")
	flag.Parse()

	var server *Server
	if *apexFlag {
		server = NewServer(ApexAddr, true)
		if *addrFlag == ApexAddr {
			slog.Info("addr is fixed on the apex server [127.0.0.1:3000]")
		}
	} else {
		server = NewServer(*addrFlag, false)
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
