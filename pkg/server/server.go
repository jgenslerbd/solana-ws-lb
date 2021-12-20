package server

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

type clientConnectionState struct {
	stateMessages []wsMessage
	mainConn      *upstreamConnection
	backupConn    *upstreamConnection
}

type ServerOpts struct {
	// Server will serve all websocket requests from this server unless considered unhealthy
	Main *url.URL
	// Server will serve all websocker requsts from provided Main is considered unhealthy
	Backup *url.URL
}

type Server struct {
	opts     *ServerOpts
	upgrader websocket.Upgrader
}

func NewServer(opts *ServerOpts) Server {
	return Server{
		opts:     opts,
		upgrader: websocket.Upgrader{},
	}
}

func (s *Server) HandlerFunc(rw http.ResponseWriter, r *http.Request) {
	client, err := s.upgrader.Upgrade(rw, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer client.Close()

	// initalize both connections
	connState := clientConnectionState{
		mainConn:   newUpstreamConnection(s.opts.Main),
		backupConn: newUpstreamConnection(s.opts.Backup),
	}
	go func() {
		// TODO shut this down
		for {
			select {
			case upstreamMsg := <-connState.mainConn.ResponseChan():
				log.Printf("responding: %s", upstreamMsg.p)
				err := client.WriteMessage(upstreamMsg.messageType, upstreamMsg.p)
				if err != nil {
					log.Printf("failed to write message: %v", err)
				}
			case upstreamMsg := <-connState.backupConn.ResponseChan():
				log.Printf("responding: %s", upstreamMsg.p)
				if connState.mainConn.GetState() == UpstreamStateUnavailable {
					log.Printf("responding with backup connection message")
					client.WriteMessage(upstreamMsg.messageType, upstreamMsg.p)
				}
			}
		}
	}()

	// TODO because reads are JSONRPC, use ids to keep track of state
	for {
		// TODO move read to goroutine?
		// read + buffer message
		var message wsMessage
		message.messageType, message.p, err = client.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		// log.Printf("server recv: %s", message.p)

		// if message is a subscription message, send to both upstreams
		// TODO: only store some messages, make this more modular
		if strings.Contains(string(message.p), "subscribe") {
			connState.mainConn.AddStateMessage(message)
			connState.backupConn.AddStateMessage(message)
		} else { // else if message is request/response, find a healthy upstream and send there
			// check if either connection available
			var upstreamConn *upstreamConnection
			if connState.mainConn.GetState() == UpstreamStateReady {
				upstreamConn = connState.mainConn
			} else if connState.backupConn.GetState() == UpstreamStateReady {
				upstreamConn = connState.backupConn
			}

			// close client connection if both backends are down
			// no sense in waiting and pushing more state into this load balancer
			// let client handle when bad things happen
			if upstreamConn == nil {
				log.Printf("no upstream connections available, closing client and upstream connections")
				connState.mainConn.Close()
				connState.backupConn.Close()
				client.Close()
				return
			}

			// TODO figure out request/response race condition and flipping between the two
			// send message with ready
			err = upstreamConn.Send(message)
			if err != nil {
				log.Println("write:", err)
				break
			}
		}
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.HandlerFunc)
}
