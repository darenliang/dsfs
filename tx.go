package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"io"
	"net/http"
	"time"
)

type Stat struct {
	Mtim time.Time `json:"mtim"`
	Ctim time.Time `json:"ctim"`
	Size int64     `json:"size"`
}

type Tx struct {
	Stat    Stat     `json:"stat"`
	Path    string   `json:"path"`
	FileIDs []string `json:"ids"`
	Tx      int      `json:"tx"`
	Type    int      `json:"file"`
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

func applyMessageTxs(db *DB, ms []*discordgo.Message) {
	fmt.Printf("Applying %d messages with TXs\n", len(ms))
	for _, m := range ms {
		if len(m.Attachments) == 0 {
			continue
		}

		resp, err := http.Get(m.Attachments[0].URL)
		if err != nil {
			fmt.Printf("%s, skipping tx batch\n", err)
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
				fmt.Printf("%s, skipping tx\n", err)
				continue
			}
			switch tx.Tx {
			case WriteTx:
				fmt.Println("Write", tx.Path)
				db.radix, _, _ = db.radix.Insert([]byte(tx.Path), tx)
			case DeleteTx:
				fmt.Println("Delete", tx.Path)
				db.radix, _, _ = db.radix.Delete([]byte(tx.Path))
			default:
				fmt.Printf("found unknown tx type %d\n, skipping tx", tx.Tx)
				continue
			}
		}

		resp.Body.Close()
	}
	fmt.Println("Done applying TXs")
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Not ready to accept TXs
	if !txReady.Load() {
		return
	}

	// Don't handle the bot's own TXs or listen to a non-TX channel
	if m.Author.ID == s.State.User.ID {
		return
	}

	if m.ChannelID != TxChannelID {
		return
	}

	// There is potentially some issues when doing this
	// In this current state, open files will not be affected
	// by any TXs broadcasted by remote clients
	applyMessageTxs(db, []*discordgo.Message{m.Message})
}
