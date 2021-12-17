package twitch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/monstercat/golib/logger"
)

var (
	ErrInvalidPassword = errors.New("attempting to connect with an invalid password")
	ErrNotConnected    = errors.New("not connected")
	ErrTimeout         = errors.New("timeout")
	ErrNoUsername      = errors.New("attempting to connect with no username")
)

// TODO: Move to own package. This can be easily made to be agnostic to twitch
const (
	IrcServer = "irc.chat.twitch.tv"
	IrcPort   = 6667

	CmdPass  = "PASS"
	CmdNick  = "NICK"
	CmdJoin  = "JOIN"
	CmdLeave = "PART"
	CmdSay   = "PRIVMSG"
	CmdPong  = "PONG"

	// DefaultRate assumes a non-verified bot sending messages in channels
	// where the user is not a moderator or broadcaster.
	// - 20 per 30 seconds for sending messages
	// - 20 authenticate attempts per 10 seconds per user
	// - 20 join attempts per 10 seconds per user
	//
	// Since the bot does not delineate between different types of commands in
	// IIRCBot.Run, we will use the slowest rate (20 per 30 secs)
	//
	// https://dev.twitch.tv/docs/irc/guide
	DefaultRate = time.Second * 30 / 20
)

type IIRCBot struct {
	net.Conn
	logger.Logger

	// killConn will be set when the connection is killed.
	killConn chan struct{}

	MsgDelay time.Duration
	Queue    chan IIRCBotCommand

	// Flags
	mu        sync.RWMutex
	reconnect bool
	sendPong  bool

	// Last username and password
	username string
	password string

	IIRCBotEventManager
}

func (b *IIRCBot) setReconnect() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.reconnect = true
	b.sendPong = false
}

func (b *IIRCBot) setSendPong() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sendPong = true
}

func (b *IIRCBot) clearSendPong() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sendPong = false
}

func (b *IIRCBot) NeedsReconnect() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.reconnect
}

func (b *IIRCBot) ClearReconnect() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.reconnect = false
}

func (b *IIRCBot) needsSendPong() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.sendPong
}

func (b *IIRCBot) validPassword(pwd string) bool {
	return pwd != "" && strings.Index(pwd, "oauth:") == 0
}

func (b *IIRCBot) send(cmd string, args string) error {
	msg := fmt.Sprintf("%s %s\r\n", cmd, args)
	b.Log(logger.SeverityInfo, "Sending Command: "+msg)
	_, err := b.Conn.Write([]byte(msg))
	return err
}

func (b *IIRCBot) Close() {
	if b.Conn == nil {
		return
	}

	if err := b.Conn.Close(); err != nil {
		b.Log(logger.SeverityError, "Error closing IRRC bot. "+err.Error())
	}
	b.Conn = nil
	close(b.killConn)
}

func (b *IIRCBot) connect(username, password string) error {
	if username == "" {
		return ErrNoUsername
	}
	if !b.validPassword(password) {
		return ErrInvalidPassword
	}

	// Close any existing connections
	b.Close()

	addr := fmt.Sprintf("%s:%d", IrcServer, IrcPort)
	b.Log(logger.SeverityInfo, "Connecting to "+addr)

	// Start a new one.
	dialer := net.Dialer{
		Timeout: time.Second,
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return err
	}
	b.Conn = conn
	b.killConn = make(chan struct{})

	// Listen to the connection!
	go b.listen()

	if err := b.send(CmdPass, password); err != nil {
		b.Close()
		return err
	}
	if err := b.send(CmdNick, username); err != nil {
		b.Close()
		return err
	}
	return nil
}

// listen to the connection. This is called directly through connect and should
// be called after b.Conn and b.killConn are created.
func (b *IIRCBot) listen() {

	buf := make([]byte, 0, 4096)

	for {
		select {
		case <-b.killConn:
			return
		case <-time.After(time.Millisecond * 100):
		}

		tmp := make([]byte, 512)
		n, err := b.Conn.Read(tmp)

		// NOTE that the following errors will cause any commands still in the queue to error
		// out while it reconnects.
		if err != nil {
			b.setReconnect()

			if err == io.EOF {
				b.Log(logger.SeverityError, "Reached EOF. Reconnecting")
			} else if err != nil {
				b.Log(logger.SeverityError, "Error reading. "+err.Error())
			}
			break
		}

		// Add to the buffer.
		buf = append(buf, tmp[:n]...)

		// Loop through all \r\n
		for {
			// Find \r\n
			idx := bytes.Index(buf, []byte("\r\n"))
			if idx == -1 {
				break
			}

			curr := strings.TrimSpace(string(buf[:idx+2]))
			buf = buf[idx+2:]

			switch {
			case strings.Index(curr, "PING") > -1:
				b.setSendPong()
			case strings.Index(curr, "PRIVMSG") > -1:
				b.processPrivMsg(curr)
			}
		}

	}
}

var privMsgRegexp = regexp.MustCompile("PRIVMSG #(?P<Channel>[A-z-]+) :(?P<Message>.*)$")

type PrivMessage struct {
	Channel string
	Message string
}

// processPrivMsg processes a private message on a twitch chat. A PRIVMSG is expected to look like this:
// :{user}!{user}@{user}.tmi.twitch.tv PRIVMSG #{channel} :{message}
//
// In the future, we can make this handle different channel commands if we need.
func (b *IIRCBot) processPrivMsg(msg string) {
	matches := privMsgRegexp.FindStringSubmatch(msg)

	p := PrivMessage{}
	p.Channel = matches[privMsgRegexp.SubexpIndex("Channel")]
	p.Message = matches[privMsgRegexp.SubexpIndex("Message")]

	b.InvokeMessageListeners(p)
}

// Connect is used to connect and authenticate to IIRC.
// It requires the oauth access token as the password in the format oath:<token>.
// In the case of failure, uses exponential backoff.
//
// https://dev.twitch.tv/docs/irc/guide
func (b *IIRCBot) Connect(username, password string) error {
	b.username = username
	b.password = password

	delay := time.Second / 2

	for {
		if delay > time.Second*16 {
			b.Log(logger.SeverityError, "Timed out.")
			return ErrTimeout
		}

		err := b.connect(username, password)
		if err == nil {
			return nil
		}

		b.Log(logger.SeverityError, fmt.Sprintf("Could not login. Waiting for %d seconds. %s", delay*2, err.Error()))
		delay = delay * 2
	}
}

// Reconnect is used to refresh the password used by the IIRCBot. Since it is using OAUTH,
// the password will expire and will need to be replaced. In this case, all commands will need to
// wait until reconnection is finished.
func (b *IIRCBot) Reconnect(username, password string) {
	b.Queue <- &IIRCBotConnect{
		Username: username,
		Password: password,
	}
}

// Send sends a command through the IIRCBot. It does this by scheduling on the queue.
func (b *IIRCBot) Send(cmd, args string) {
	b.Queue <- &IIRCBotMessage{
		Cmd:  cmd,
		Args: args,
	}
}

func (b *IIRCBot) Join(channel string) {
	b.Queue <- &IIRCBotJoin{
		Channel: channel,
	}
}

func (b *IIRCBot) Leave(channel string) {
	b.Queue <- &IIRCBotLeave{
		Channel: channel,
	}
}

func (b *IIRCBot) Say(channel, msg string) {
	b.Queue <- &IIRCBotSay{
		Channel: channel,
		Message: msg,
	}
}

// Run runs the service which actually posts the messages on the channel.
func (b *IIRCBot) Run(die <-chan struct{}) {
	delay := b.MsgDelay
	if delay == 0 {
		delay = DefaultRate
	}

	// Allow a queue of 50 items.
	b.Queue = make(chan IIRCBotCommand, 50)

	go func() {
		for {
			select {
			case <-die:
				close(b.Queue)
				return

			case <-time.After(delay):
			}

			// Check flags first! If we need to reconnect, we should do that here.
			if b.NeedsReconnect() {
				// TODO: this is no good! We need to reconnect immediately and not send it to the queue.
				//  also, we need to activate event listeners on reconnect.
				//  finally, those listeners should be able to immediately send join commands after a successful
				//  reconnection.
				if err := b.Connect(b.username, b.password); err != nil {
					// We could not reconnect! Produce an error.
					// Conn should still be null.
					// EMAIL error

					time.Sleep(time.Minute * 120)
					continue
				}
				b.InvoiceRejoinListeners()
				b.ClearReconnect()

				continue
			}
			if b.needsSendPong() {
				if err := b.send(CmdPong, ":tmi.twitch.tv"); err != nil {
					b.Log(logger.SeverityError, "Could not send pong. "+err.Error())
				}
				b.clearSendPong()
			}

			// Run a queue command if there are any.
			if len(b.Queue) > 0 {
				cmd := <-b.Queue
				if err := cmd.Run(b); err != nil {
					l := &logger.Contextual{
						Logger:  b,
						Context: cmd.String(),
					}

					// TODO: if this is a connection command, we need to do more than just log SE
					l.Log(logger.SeverityError, "Error running command. "+err.Error())
				}
			}
		}
	}()
}
