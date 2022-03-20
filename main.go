package main

import (
	"flag"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"github.com/getlantern/systray"
)

var (
	token   string
	guildID string
)

func init() {
	flag.StringVar(&token, "t", "", "Bot Token")
	flag.StringVar(&guildID, "s", "", "Guild ID")
	flag.Parse()
}

func main() {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	dg.AddHandler(messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages

	err = dg.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	db, err := setupDB(dg, guildID)
	if err != nil {
		fmt.Println(err)
		return
	}

	go func() {
		systray.Run(onReady, onExit)
	}()

	dsfs := NewDsfs(dg, db)
	host := fuse.NewFileSystemHost(dsfs)
	host.SetCapReaddirPlus(true)
	host.Mount("", nil)
}
