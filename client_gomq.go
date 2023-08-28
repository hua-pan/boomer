//go:build !goczmq
// +build !goczmq

package boomer

import (
	"fmt"
	"log"

	"github.com/myzhan/gomq"
	"github.com/myzhan/gomq/zmtp"
)

type gomqSocketClient struct {
	masterHost string
	masterPort int
	identity   string

	dealerSocket gomq.Dealer

	fromMaster             chan message
	toMaster               chan message
	disconnectedFromMaster chan bool
	shutdownChan           chan bool
}

func newClient(masterHost string, masterPort int, identity string) (client *gomqSocketClient) {
	log.Println("Boomer is built with gomq support.")
	client = &gomqSocketClient{
		masterHost:             masterHost,
		masterPort:             masterPort,
		identity:               identity,
		fromMaster:             make(chan message, 100),
		toMaster:               make(chan message, 100),
		disconnectedFromMaster: make(chan bool),
		shutdownChan:           make(chan bool),
	}
	return client
}

func (c *gomqSocketClient) connect() (err error) {
	addr := fmt.Sprintf("tcp://%s:%d", c.masterHost, c.masterPort)
	c.dealerSocket = gomq.NewDealer(zmtp.NewSecurityNull(), c.identity)

	if err = c.dealerSocket.Connect(addr); err != nil {
		return err
	}

	log.Printf("Boomer is connected to master(%s) press Ctrl+c to quit.\n", addr)
	go c.recv()
	go c.send()

	return nil
}

func (c *gomqSocketClient) close() {
	close(c.shutdownChan)
	if c.dealerSocket != nil {
		c.dealerSocket.Close()
	}
}

func (c *gomqSocketClient) recvChannel() chan message {
	return c.fromMaster
}

func (c *gomqSocketClient) recv() {
	for {
		select {
		case <-c.shutdownChan:
			return
		case msg := <-c.dealerSocket.RecvChannel():
			if msg.MessageType == zmtp.CommandMessage {
				continue
			}
			if len(msg.Body) == 0 {
				continue
			}
			body, err := msg.Body[0], msg.Err
			if err != nil {
				log.Printf("Error reading: %v\n", err)
				continue
			}

			decodedGenericMsg, err := newGenericMessageFromBytes(body)
			if err == nil {
				if decodedGenericMsg.NodeID != c.identity {
					log.Printf("Recv a %s message for node(%s), not for me(%s), dropped.\n", decodedGenericMsg.Type, decodedGenericMsg.NodeID, c.identity)
				} else {
					c.fromMaster <- decodedGenericMsg
				}
				continue
			}

			decodedCustomMsg, err := newCustomMessageFromBytes(body)
			if err == nil {
				if decodedCustomMsg.NodeID != c.identity {
					log.Printf("Recv a %s message for node(%s), not for me(%s), dropped.\n", decodedCustomMsg.Type, decodedCustomMsg.NodeID, c.identity)
				} else {
					c.fromMaster <- decodedCustomMsg
				}
				continue
			}

			log.Printf("Msgpack decode fail: %v\n", err)
		}
	}
}

func (c *gomqSocketClient) sendChannel() chan message {
	return c.toMaster
}

func (c *gomqSocketClient) send() {
	for {
		select {
		case <-c.shutdownChan:
			return
		case msg := <-c.toMaster:
			c.sendMessage(msg)

			// If we send a genericMessage and the message type is quit, we need to disconnect.
			m, ok := msg.(*genericMessage)
			if ok {
				if m.Type == "quit" {
					c.disconnectedFromMaster <- true
				}
			}
		}
	}
}

func (c *gomqSocketClient) sendMessage(msg message) {
	serializedMessage, err := msg.serialize()
	if err != nil {
		log.Printf("Msgpack encode fail: %v\n", err)
		return
	}
	err = c.dealerSocket.Send(serializedMessage)
	if err != nil {
		c.fromMaster <- newGenericMessage("quit", nil, "")
		log.Printf("send message to master error, must quit worker, err=%v\n", err)
	}
}

func (c *gomqSocketClient) disconnectedChannel() chan bool {
	return c.disconnectedFromMaster
}
