package main

import (
	"bufio"
	"bytes"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
)

type Tx struct {
	Ctim      time.Time `json:"ctim,omitempty"`
	Mtim      time.Time `json:"mtim,omitempty"`
	Path      string    `json:"path"`
	FileIDs   []string  `json:"ids,omitempty"`
	Checksums []string  `json:"sums,omitempty"`
	Tx        TxType    `json:"tx"`
	Type      InodeType `json:"type,omitempty"`
	Size      int64     `json:"size,omitempty"`
}

// getDataFile downloads an attachment and writes to buffer
func getDataFile(channelID string, fileID string, buffer []byte) (int, error) {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(fmt.Sprintf("https://cdn.discordapp.com/attachments/%s/%s/%s", channelID, fileID, DataChannelName))
	req.Header.SetMethod(fasthttp.MethodGet)
	resp := fasthttp.AcquireResponse()
	err := fasthttp.Do(req, resp)
	fasthttp.ReleaseRequest(req)
	if err != nil {
		return 0, err
	}
	n := copy(buffer, resp.Body())
	fasthttp.ReleaseResponse(resp)
	return n, nil
}

// createDeleteTx creates a delete transaction for path
func createDeleteTx(path string) Tx {
	return Tx{Tx: DeleteTx, Path: path}
}

// applyMessageTxs applies transactions to DB, and writes message data to buffer if a buffer is given
func applyMessageTxs(db DB, ms []*discordgo.Message, buffer *bytes.Buffer, live bool) {
	zap.S().Infof("applying %d messages with TXs", len(ms))
	for _, m := range ms {
		for _, file := range m.Attachments {
			_, data, err := fasthttp.Get(nil, file.URL)
			if err != nil {
				zap.S().Warnf("%s, skipping tx batch", err)
				continue
			}

			bytesReader := bytes.NewReader(data)
			scanner := bufio.NewScanner(bytesReader)

			for scanner.Scan() {
				line := scanner.Text()
				if len(line) == 0 {
					continue
				}
				tx := &Tx{}
				err := json.Unmarshal([]byte(line), tx)
				if err != nil {
					zap.S().Warnf("%s, skipping tx", err)
					continue
				}

				switch tx.Tx {
				case WriteTx:
					zap.S().Debugw("Write", "path", tx.Path)
					if live {
						err := dsfs.ApplyLiveTx(tx.Path, tx)
						if err != nil {
							zap.S().Warnw("failed to apply live tx", "error", err)
						}
					} else {
						db.Insert(tx.Path, tx)
					}
				case DeleteTx:
					zap.S().Debugw("Delete", "path", tx.Path)
					if live {
						dsfs.lock.Lock()
						db.Delete(tx.Path)
						delete(dsfs.open, tx.Path)
						dsfs.lock.Unlock()
					} else {
						db.Delete(tx.Path)
					}
				default:
					zap.S().Warnf("found unknown tx type %d, skipping tx", tx.Tx)
					continue
				}

				// Write to buffer
				if buffer != nil {
					buffer.WriteString(line)
					buffer.WriteByte('\n')
				}
			}
		}
	}
	zap.S().Info("done applying TXs")
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Not ready to accept TXs
	if !dsfsReady.Load() {
		return
	}

	// Don't handle the bot's own TXs or listen to a non-TX channel
	if m.Author.ID == s.State.User.ID || m.ChannelID != dsfs.txChannel.ID {
		return
	}

	// There is potentially some issues when doing this
	// In this current state, open files will not be affected
	// by any TXs broadcasted by remote clients
	applyMessageTxs(dsfs.db, []*discordgo.Message{m.Message}, nil, true)
}
