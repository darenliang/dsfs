package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"io"
	"net/http"
	"time"
)

type Tx struct {
	Ctim      time.Time `json:"ctim,omitempty"`
	Mtim      time.Time `json:"mtim,omitempty"`
	Path      string    `json:"path"`
	FileIDs   []string  `json:"ids,omitempty"`
	Checksums []string  `json:"sums,omitempty"`
	Tx        int       `json:"tx"`
	Type      int       `json:"type,omitempty"`
	Size      int64     `json:"size,omitempty"`
}

func getDataFile(channelID string, fileID string, buffer []byte) (int, error) {
	resp, err := http.Get(fmt.Sprintf("https://cdn.discordapp.com/attachments/%s/%s/%s", channelID, fileID, DataChannelName))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	n, err := io.ReadFull(resp.Body, buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return 0, err
	}
	return n, nil
}

func createDeleteTx(path string) Tx {
	return Tx{Tx: DeleteTx, Path: path}
}

func applyMessageTxs(db *DB, ms []*discordgo.Message, buffer *bytes.Buffer, live bool) {
	logger.Infof("applying %d messages with TXs", len(ms))
	for _, m := range ms {
		if len(m.Attachments) == 0 {
			continue
		}

		resp, err := http.Get(m.Attachments[0].URL)
		if err != nil {
			logger.Warnf("%s, skipping tx batch", err)
			continue
		}

		scanner := bufio.NewScanner(resp.Body)

		for scanner.Scan() {
			line := scanner.Text()
			if len(line) == 0 {
				continue
			}
			tx := Tx{}
			err := json.Unmarshal([]byte(line), &tx)
			if err != nil {
				logger.Warnf("%s, skipping tx", err)
				continue
			}

			pathBytes := []byte(tx.Path)

			switch tx.Tx {
			case WriteTx:
				logger.Debug("Write", tx.Path)
				if live {
					err := dsfs.ApplyLiveTx(pathBytes, tx)
					if err != nil {
						logger.Warn("failed to apply live tx", err)
					}
				} else {
					db.radix, _, _ = db.radix.Insert(pathBytes, tx)
				}
			case DeleteTx:
				logger.Debug("Delete", tx.Path)
				if live {
					dsfs.lock.Lock()
					db.radix, _, _ = db.radix.Delete(pathBytes)
					delete(dsfs.open, tx.Path)
					dsfs.lock.Unlock()
				} else {
					db.radix, _, _ = db.radix.Delete(pathBytes)
				}
			default:
				logger.Warnf("found unknown tx type %d, skipping tx", tx.Tx)
				continue
			}

			// Write to buffer
			if buffer != nil {
				buffer.WriteString(line)
				buffer.WriteByte('\n')
			}
		}

		resp.Body.Close()
	}
	logger.Info("done applying TXs")
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Not ready to accept TXs
	if !dbReady.Load() {
		return
	}

	// Don't handle the bot's own TXs or listen to a non-TX channel
	if m.Author.ID == s.State.User.ID {
		return
	}

	logger.Info(m.Author.Username)
	if m.Author.Username == UniqueID {
		return
	}

	if m.ChannelID != TxChannelID {
		return
	}

	// There is potentially some issues when doing this
	// In this current state, open files will not be affected
	// by any TXs broadcasted by remote clients
	applyMessageTxs(db, []*discordgo.Message{m.Message}, nil, true)
}
