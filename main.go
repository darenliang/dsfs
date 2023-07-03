package main

import (
	"fmt"
	"github.com/mattn/go-colorable"
	"go.uber.org/zap/zapcore"
	"os"
	"sync/atomic"

	"github.com/alecthomas/kingpin/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/pprofhandler"
	"go.uber.org/zap"
)

var (
	token     string
	userToken bool
	guildID   string
	mount     string
	compact   bool
	cacheType string
	dbType    string
	debug     bool
	port      int
	options   []string
	// We need to jankily expose dsfs for event handlers
	dsfs      *Dsfs
	dsfsReady = &atomic.Bool{}
)

func main() {
	kingpin.Flag("token", "Token").Short('t').StringVar(&token)
	kingpin.Flag("server", "Guild ID").Short('s').StringVar(&guildID)
	kingpin.Flag("user", "Token is a user token").Short('u').BoolVar(&userToken)
	kingpin.Flag("mount", "Mount point").Short('m').StringVar(&mount)
	kingpin.Flag("compact", "Compact transactions").Short('x').BoolVar(&compact)
	kingpin.Flag("cache", "Cache type").Short('c').Default("disk").EnumVar(&cacheType, "disk", "memory")
	kingpin.Flag("db", "Database type").Short('d').Default("radix").EnumVar(&dbType, "radix", "map")
	kingpin.Flag("verbose", "Enable pprof and print debug logs").Short('v').BoolVar(&debug)
	kingpin.Flag("port", "Port to run pprof on").Short('p').Default("8000").IntVar(&port)
	kingpin.Flag("options", "FUSE options").Short('o').StringsVar(&options)
	kingpin.Parse()

	if token == "" {
		token = os.Getenv("DSFS_TOKEN")
	}

	if guildID == "" {
		guildID = os.Getenv("DSFS_SERVER")
	}

	if token == "" || guildID == "" {
		zap.S().Error("token and guild id are required")
		return
	}

	// Setup logger and debug endpoint if specified
	config := zap.NewDevelopmentEncoderConfig()
	config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(config),
		zapcore.AddSync(colorable.NewColorableStdout()),
		zapcore.DebugLevel,
	))
	if debug {
		zap.ReplaceGlobals(logger)
		go func() {
			zap.S().Infof("pprof running on port %d", port)
			_ = fasthttp.ListenAndServe(fmt.Sprintf(":%d", port), pprofhandler.PprofHandler)
		}()
	} else {
		zap.ReplaceGlobals(logger.WithOptions(zap.IncreaseLevel(zap.InfoLevel)))
	}
	defer logger.Sync()

	var tokenPrefix string
	if !userToken {
		tokenPrefix = "Bot "
	}

	dg, err := discordgo.New(tokenPrefix + token)
	if err != nil {
		zap.S().Error("error creating Discord session,", err)
		return
	}

	dg.AddHandler(messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages

	err = dg.Open()
	if err != nil {
		zap.S().Error("error opening connection,", err)
		return
	}

	txChannel, dataChannel, err := prepareChannels(dg, guildID)
	if err != nil {
		zap.S().Error(err)
		return
	}

	db, err := setupDB(dg, txChannel, compact, dbType)
	if err != nil {
		zap.S().Error(err)
		return
	}

	writer := setupWriter(dg, txChannel.ID, dataChannel.ID)

	dsfs = NewDsfs(dg, db, writer, txChannel, dataChannel, cacheType)
	dsfsReady.Store(true)

	host := fuse.NewFileSystemHost(dsfs)
	host.SetCapReaddirPlus(true)
	host.Mount(mount, FuseArgs(options))
}
