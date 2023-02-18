package main

import (
	"bytes"
	"time"

	"github.com/bwmarrin/discordgo"
)

type QueueItem struct {
	channel chan QueueResult
	data    []byte
}

type QueueResult struct {
	err    error
	fileID string
}

type Writer struct {
	txQueue   chan QueueItem
	dataQueue chan QueueItem
}

func (w *Writer) SendTx(data []byte) (string, error) {
	return w.sendToQueue(data, w.txQueue)
}

func (w *Writer) SendData(data []byte) (string, error) {
	return w.sendToQueue(data, w.dataQueue)
}

func (w *Writer) sendToQueue(data []byte, queue chan<- QueueItem) (string, error) {
	callback := make(chan QueueResult)
	queue <- QueueItem{
		data:    data,
		channel: callback,
	}
	result := <-callback
	return result.fileID, result.err
}

func (w *Writer) processQueue(dg *discordgo.Session, filename string, channelID string, queue <-chan QueueItem) {
	go func() {
		var onhold []QueueItem
		for {
			items := onhold
			onhold = nil
			after := time.After(QueueTimeout)
			for loop, i, totalSize := true, 0, 0; loop && i < MaxDiscordFileCount; i++ {
				select {
				case item := <-queue:
					totalSize += len(item.data)
					if totalSize > MaxDiscordFileSize {
						onhold = append(onhold, item)
						loop = false
					} else {
						items = append(items, item)
					}
				case <-after:
					loop = false
				}
			}

			if len(items) == 0 {
				continue
			}

			var files []*discordgo.File
			for _, item := range items {
				files = append(files, &discordgo.File{
					Name:   filename,
					Reader: bytes.NewReader(item.data),
				})
			}
			msg, err := dg.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Files: files})
			for i := 0; i < len(items); i++ {
				if err != nil {
					items[i].channel <- QueueResult{
						err: err,
					}
				} else {
					items[i].channel <- QueueResult{
						fileID: msg.Attachments[i].ID,
					}
				}
			}
		}
	}()
}

func (w *Writer) ProcessTxQueue(dg *discordgo.Session, channelID string) {
	w.processQueue(dg, TxChannelName, channelID, w.txQueue)
}

func (w *Writer) ProcessDataQueue(dg *discordgo.Session, channelID string) {
	w.processQueue(dg, DataChannelName, channelID, w.dataQueue)
}

func setupWriter(dg *discordgo.Session, txChannelID, dataChannelID string) *Writer {
	writer := &Writer{
		txQueue:   make(chan QueueItem),
		dataQueue: make(chan QueueItem),
	}
	writer.ProcessDataQueue(dg, dataChannelID)
	writer.ProcessTxQueue(dg, txChannelID)
	return writer
}
