package twitch

import (
	"fmt"
	"strings"
)

type IIRCBotCommand interface {
	// Run runs the command
	Run(b *IIRCBot) error

	// String is a string representation of the command.
	String() string
}

// IIRCBotMessage is a type of IIRCBotCommand which sends a specific message through the
// bot connection.
type IIRCBotMessage struct {
	Cmd  string
	Args string
}

func (m *IIRCBotMessage) String() string {
	return fmt.Sprintf("[Message] %s %s", m.Cmd, m.Args)
}

func (m *IIRCBotMessage) Run(bot *IIRCBot) error {
	return bot.send(m.Cmd, m.Args)
}

// IIRCBotConnect is a type of IIRCBotCommand which tells the IIRCBot to reconnect.
type IIRCBotConnect struct {
	Username string
	Password string
}

func (m *IIRCBotConnect) String() string {
	return fmt.Sprintf("[Connect] %s", m.Username)
}

func (m *IIRCBotConnect) Run(bot *IIRCBot) error {
	// Close any existing connection
	bot.Close()
	return bot.Connect(m.Username, m.Password)
}

// IIRCBotSay is a command that says a message on the channel. If the channel hasn't been
// joined, it will make sure to join the channel first.
type IIRCBotSay struct {
	Message string
	Channel string
}

func (m *IIRCBotSay) String() string {
	return fmt.Sprintf("[Say] %s; %s", m.Channel, m.Message)
}

func (m *IIRCBotSay) Run(bot *IIRCBot) error {
	channel := strings.ToLower(m.Channel)
	return bot.send(CmdSay, fmt.Sprintf("#%s :%s", channel, m.Message))
}

// IIRCBotJoin is a command to join a channel.
type IIRCBotJoin struct {
	Channel string
}

func (m *IIRCBotJoin) String() string {
	return fmt.Sprintf("[Join] %s", m.Channel)
}

func (m *IIRCBotJoin) Run(bot *IIRCBot) error {
	channel := "#" + strings.ToLower(m.Channel)
	return bot.send(CmdJoin, channel)
}

// IIRCBotLeave will leave a channel
type IIRCBotLeave struct {
	Channel string
}

func (m *IIRCBotLeave) String() string {
	return fmt.Sprintf("[Leave] %s", m.Channel)
}

func (m *IIRCBotLeave) Run(bot *IIRCBot) error {
	channel := "#" + strings.ToLower(m.Channel)
	return bot.send(CmdLeave, channel)
}