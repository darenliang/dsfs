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
	Size int64     `json:"size"`
	Mtim time.Time `json:"mtim"`
	Ctim time.Time `json:"ctim"`
}

type Tx struct {
	Tx      int      `json:"tx"`
	Path    string   `json:"path"`
	Type    int      `json:"file"`
	FileIDs []string `json:"ids"`
	Stat    Stat     `json:"stat"`
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
	if m.Author.ID == s.State.User.ID {
		return
	}
	fmt.Println(m.Content)
}
