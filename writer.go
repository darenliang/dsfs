package main

import (
	"errors"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/atomic"
	"io"
	"math/rand"
)

var (
	writerReady = atomic.NewBool(false)
)

type Writer interface {
	Send(channelID, name string, r io.Reader) (*discordgo.Message, error)
	UpdateWebhooks(channelID string) error
}

type WebhookWriter struct {
	dg           *discordgo.Session
	txWebhooks   []*discordgo.Webhook
	txIndex      int
	dataWebhooks []*discordgo.Webhook
	dataIndex    int
}

func (w *WebhookWriter) Send(channelID, name string, r io.Reader) (*discordgo.Message, error) {
	file := &discordgo.File{
		Name:        name,
		ContentType: "",
		Reader:      r,
	}

	var webhook *discordgo.Webhook
	switch channelID {
	case TxChannelID:
		webhook = w.txWebhooks[w.txIndex]
		w.txIndex = (w.txIndex + 1) % len(w.txWebhooks)
	case DataChannelID:
		webhook = w.dataWebhooks[w.dataIndex]
		w.dataIndex = (w.dataIndex + 1) % len(w.dataWebhooks)
	default:
		return nil, errors.New(fmt.Sprintf("not a valid channel id: %s", channelID))
	}

	execute, err := w.dg.WebhookExecute(
		webhook.ID,
		webhook.Token,
		true,
		&discordgo.WebhookParams{
			Username: UniqueID,
			Files:    []*discordgo.File{file},
		})

	if err != nil {
		logger.Error(err)
		return nil, err
	}

	return execute, err
}

func (w *WebhookWriter) UpdateWebhooks(channelID string) error {
	switch channelID {
	case TxChannelID:
		webhooks, err := w.dg.ChannelWebhooks(channelID)
		if err != nil {
			return err
		}
		if len(webhooks) == 0 {
			return errors.New(fmt.Sprintf("%s channel has no webhooks", TxChannelName))
		}
		w.txWebhooks = webhooks
		w.txIndex = rand.Intn(len(webhooks))
	case DataChannelID:
		webhooks, err := w.dg.ChannelWebhooks(channelID)
		if err != nil {
			return err
		}
		if len(webhooks) == 0 {
			return errors.New(fmt.Sprintf("%s channel has no webhooks", DataChannelName))
		}
		w.dataWebhooks = webhooks
		w.dataIndex = rand.Intn(len(webhooks))
	}
	return nil
}

type BotWriter struct {
	dg *discordgo.Session
}

func (w *BotWriter) Send(channelID, name string, r io.Reader) (*discordgo.Message, error) {
	return w.dg.ChannelFileSend(channelID, name, r)
}

func (w *BotWriter) UpdateWebhooks(channelID string) error {
	return nil
}

func setupWriter(s *discordgo.Session, webhook bool) (Writer, error) {
	defer writerReady.Store(true)

	if !webhook {
		logger.Info("configuring send files via bot/user")
		return &BotWriter{dg: s}, nil
	}

	UniqueID = uuid.NewString()

	writer := &WebhookWriter{dg: s}
	err := writer.UpdateWebhooks(TxChannelID)
	if err != nil {
		return nil, err
	}
	err = writer.UpdateWebhooks(DataChannelID)
	if err != nil {
		return nil, err
	}

	logger.Info("configuring send files via webhooks")
	return writer, nil
}

func webhooksUpdate(s *discordgo.Session, wu *discordgo.WebhooksUpdate) {
	err := writer.UpdateWebhooks(wu.ChannelID)
	if err != nil {
		panic(err)
	}
}
