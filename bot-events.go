package twitch

import "sync"

type IIRCBotPrivMsgEventListener func(message PrivMessage)
type IIRCBotRejoinListener func()

// IIRCBotEventManager managers events for the bot.
type IIRCBotEventManager struct {
	mu sync.RWMutex

	privMsgListeners []IIRCBotPrivMsgEventListener
	rejoinListeners []IIRCBotRejoinListener
}

func (m *IIRCBotEventManager) AddMessageListener(listener IIRCBotPrivMsgEventListener) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.privMsgListeners = append(m.privMsgListeners, listener)
}

func (m *IIRCBotEventManager) AddRejoinListener(listener IIRCBotRejoinListener) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rejoinListeners = append(m.rejoinListeners, listener)
}

func (m *IIRCBotEventManager) InvokeMessageListeners(msg PrivMessage) {
	for _, l := range m.privMsgListeners {
		l(msg)
	}
}

func (m *IIRCBotEventManager) InvokeRejoinListeners() {
	for _, l := range m.rejoinListeners {
		l()
	}
}