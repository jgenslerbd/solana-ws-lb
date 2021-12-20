package server

import (
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gorilla/websocket"
)

type wsMessage struct {
	messageType int
	p           []byte
}

type UpstreamState int

const (
	// UpstreamStateUnavailable describes when a connection is not functioning properly but should be available
	UpstreamStateUnavailable = iota
	// UpstreamStateReady describes when a connection is working and can service Send() requests
	UpstreamStateReady
	// UpstreamStateClosed describes when a connection will never open again
	UpstreamStateClosed
)

type Upstream interface {
	GetState() UpstreamState
	AddStateMessage(msg wsMessage)
	Send(msg wsMessage)
	Close()
}

type upstreamConnection struct {
	url               *url.URL
	state             UpstreamState
	stateMessagesLock sync.Mutex
	stateMessages     []wsMessage
	websocketConnLock sync.Mutex
	websocketConn     *websocket.Conn
	responseChan      chan wsMessage
}

func newUpstreamConnection(url *url.URL) *upstreamConnection {
	uc := upstreamConnection{
		url:          url,
		state:        UpstreamStateReady,
		responseChan: make(chan wsMessage),
	}
	uc.connect()
	go uc.reconnectLoop()
	return &uc
}

func handleWebsocketResponses(conn *websocket.Conn, responseChan chan wsMessage) {
	// TODO find better way to shut this down so error message don't flood during normal operations
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			log.Printf("error reading message from upstream: %v", err)
			return
		}
		responseChan <- wsMessage{
			messageType: messageType,
			p:           p,
		}
	}
}

func (uc *upstreamConnection) connect() error {
	conn, res, err := websocket.DefaultDialer.Dial(uc.url.String(), nil)
	if err != nil {
		log.Printf("failed to open connection to main: %v", res)
		return err
	}

	// wrap close handler to propagate state up
	previousCloseHandler := conn.CloseHandler()
	conn.SetCloseHandler(func(code int, text string) error {
		log.Printf("closing connection")
		uc.websocketConnLock.Lock()
		defer uc.websocketConnLock.Unlock()
		uc.state = UpstreamStateClosed
		return previousCloseHandler(code, text)
	})

	uc.websocketConnLock.Lock()
	defer uc.websocketConnLock.Unlock()
	uc.websocketConn = conn
	go handleWebsocketResponses(uc.websocketConn, uc.responseChan)

	return nil
}

func (uc *upstreamConnection) reconnectLoop() {
	for {
		if uc.state == UpstreamStateClosed {
			return
		}
		if uc.websocketConn == nil {
			// TODO allow parameterization
			err := backoff.Retry(uc.connect, backoff.NewExponentialBackOff())
			if err != nil {
				// Handle error.
				return
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func (uc *upstreamConnection) GetState() UpstreamState {
	return uc.state
}

func (uc *upstreamConnection) AddStateMessage(msg wsMessage) {
	uc.stateMessagesLock.Lock()
	defer uc.stateMessagesLock.Unlock()
	uc.stateMessages = append(uc.stateMessages, msg)
	// TODO turn into backoff / goroutine
	uc.Send(msg)
}

func (uc *upstreamConnection) Send(msg wsMessage) error {
	err := uc.websocketConn.WriteMessage(msg.messageType, msg.p)
	if err != nil {
		log.Println("upstream: failed to WriteMessage: %w", err)
		// set state to unavilable so other upstreams get chosen
		uc.state = UpstreamStateUnavailable
		// try to close and release any resources
		uc.websocketConnLock.Lock()
		defer uc.websocketConnLock.Unlock()
		uc.websocketConn.Close()
		// set to nil so reconnector begins
		uc.websocketConn = nil
	} else {
		uc.state = UpstreamStateReady
	}
	return err
}

func (uc *upstreamConnection) ResponseChan() chan wsMessage {
	return uc.responseChan
}

func (uc *upstreamConnection) Close() {
	uc.websocketConnLock.Lock()
	defer uc.websocketConnLock.Unlock()
	err := uc.websocketConn.Close()
	if err != nil {
		log.Printf("upstream: failed to Close connection: %v", err)
	}
}
