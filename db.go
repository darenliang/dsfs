package main

import (
	"bytes"
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/hashicorp/go-immutable-radix/v2"
	"go.uber.org/zap"
	"io"
)

// DB provides a consistent interface for implementing the in-mem database backend
type DB interface {
	Get(key string) (*Tx, bool)
	Insert(key string, value *Tx)
	Delete(key string)
	Iterator(prefix string) Iterator
}

// Iterator provides a consistent interface for implementing an iterator for DB
type Iterator interface {
	Next() (string, *Tx, bool)
}

// RadixDB implements DB backed by a radix tree
type RadixDB struct {
	radix *iradix.Tree[*Tx]
}

func (db *RadixDB) Get(key string) (*Tx, bool) {
	return db.radix.Get([]byte(key))
}

func (db *RadixDB) Insert(key string, value *Tx) {
	db.radix, _, _ = db.radix.Insert([]byte(key), value)
}

func (db *RadixDB) Delete(key string) {
	db.radix, _, _ = db.radix.Delete([]byte(key))
}

func (db *RadixDB) Iterator(prefix string) Iterator {
	iter := db.radix.Root().Iterator()
	iter.SeekPrefix([]byte(prefix))
	return &RadixDBIterator{iter: iter}
}

// RadixDBIterator implements an iterator for RadixDB
type RadixDBIterator struct {
	iter *iradix.Iterator[*Tx]
}

func (iter *RadixDBIterator) Next() (string, *Tx, bool) {
	bytesKey, tx, ok := iter.iter.Next()
	return string(bytesKey), tx, ok
}

// prepareChannels prepares the tx and data channels
// creating channels if necessary
func prepareChannels(dg *discordgo.Session, guildID string) (*discordgo.Channel, *discordgo.Channel, error) {
	channels, err := dg.GuildChannels(guildID)
	if err != nil {
		return nil, nil, err
	}

	// Find existing channels
	channelMap := map[string]*discordgo.Channel{
		DataChannelName: nil,
		TxChannelName:   nil,
	}
	for _, channel := range channels {
		if _, ok := channelMap[channel.Name]; ok {
			channelMap[channel.Name] = channel
		}
	}

	// Create missing channels
	for name, channel := range channelMap {
		if channel == nil {
			create, err := dg.GuildChannelCreate(guildID, name, discordgo.ChannelTypeGuildText)
			if err != nil {
				return nil, nil, err
			}
			channelMap[name] = create
		}
	}

	return channelMap[TxChannelName], channelMap[DataChannelName], nil
}

// setupDB setups the in-mem database
// This function needs to be refactored; it looks really gross in its current
// state.
func setupDB(dg *discordgo.Session, txChannel *discordgo.Channel, compact bool) (DB, error) {
	db := &RadixDB{radix: iradix.New[*Tx]()}

	txBuffer := &bytes.Buffer{}
	var pinnedMsg *discordgo.Message

	// We check for the lastPinTimestamp to see which TX to start from
	// this is not necessary, but if we need to speed up DB setup we can
	// compress multiple TXs into one message and re-pin to the new starting
	// TX.
	//
	// If lastPinTimestamp is not found, we insert the root folder to
	// initialize.
	if txChannel.LastPinTimestamp != nil {
		pinnedMsgs, err := dg.ChannelMessagesPinned(txChannel.ID)
		if err != nil {
			return nil, err
		}
		if len(pinnedMsgs) == 0 {
			return nil, errors.New("pin timestamp found but no pins were found, very weird")
		}

		// Get the latest pinned message
		pinnedMsg = pinnedMsgs[len(pinnedMsgs)-1]
		messages := []*discordgo.Message{pinnedMsg}
		for {
			batch, err := dg.ChannelMessages(
				txChannel.ID,
				MaxDiscordMessageRequest,
				"", messages[len(messages)-1].ID, "",
			)
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 {
				break
			}

			// Messages are in reverse order
			for i, j := 0, len(batch)-1; i < j; i, j = i+1, j-1 {
				batch[i], batch[j] = batch[j], batch[i]
			}
			messages = append(messages, batch...)

			if len(batch) != MaxDiscordMessageRequest {
				break
			}
		}

		if compact {
			zap.S().Info("compacting TXs")
			applyMessageTxs(db, messages, txBuffer, false)
		} else {
			applyMessageTxs(db, messages, nil, false)
		}
	} else {
		tx := &Tx{
			Tx:   WriteTx,
			Path: "/",
			Type: FolderType,
		}
		db.Insert("/", tx)
		b, _ := json.Marshal(tx)
		var err error
		pinnedMsg, err = dg.ChannelFileSend(
			txChannel.ID,
			TxChannelName,
			bytes.NewReader(b),
		)
		if err != nil {
			return nil, err
		}
		err = dg.ChannelMessagePin(txChannel.ID, pinnedMsg.ID)
		if err != nil {
			return nil, err
		}
	}

	// Return early if compaction is not needed
	if !compact {
		zap.S().Info("compaction not needed")
		return db, nil
	}

	messageBuffer := make([]byte, 0, MaxDiscordFileSize)
	var firstMsg *discordgo.Message
	for {
		b, err := txBuffer.ReadBytes('\n')
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			zap.S().Warnw("failed to read message buffer", "error", err)
			return db, nil
		}
		if err != nil || len(b) == 0 {
			break
		}
		if len(messageBuffer)+len(b) > MaxDiscordFileSize {
			msg, err := dg.ChannelFileSend(txChannel.ID, TxChannelName, bytes.NewReader(messageBuffer))
			if err != nil {
				zap.S().Warnw("aborting transaction compaction", "error", err)
				return db, nil
			}
			if firstMsg == nil {
				firstMsg = msg
			}
			// Keep underlying allocated memory
			messageBuffer = messageBuffer[:0]
		} else {
			messageBuffer = append(messageBuffer, b...)
		}
	}

	// Check if messageBuffer has outstanding transactions
	if len(messageBuffer) != 0 {
		msg, err := dg.ChannelFileSend(txChannel.ID, TxChannelName, bytes.NewReader(messageBuffer))
		if err != nil {
			zap.S().Warnw("aborting transaction compaction", "error", err)
			return db, nil
		}
		if firstMsg == nil {
			firstMsg = msg
		}
	}

	if firstMsg != nil {
		err := dg.ChannelMessagePin(txChannel.ID, firstMsg.ID)
		if err != nil {
			zap.S().Warn("failed to pin new transaction start point")
			return db, nil
		}
		err = dg.ChannelMessageUnpin(txChannel.ID, pinnedMsg.ID)
		if err != nil {
			zap.S().Warn("failed to unpin old transaction start point, please manually unpin old pinned messages")
			return db, nil
		}
	}

	return db, nil
}
