package telegram

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tg-drive/pkg/config"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/net/proxy"
)

const (
	DefaultChunkSize = 512 * 1024 // 512 KB per chunk
	DefaultAppID     = 2040       // Official Telegram desktop test app_id or user's custom
	DefaultAppHash   = "b18441a1ff607e10a989891a5462e627"
)

type ProgressCallback func(uploadedBytes int64, totalBytes int64, speedBytesPerSec float64)

type UploadResult struct {
	MessageID     int
	ChannelID     int64
	FileID        int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	MimeType      string
}

type Client struct {
	cfg       *config.Config
	client    *telegram.Client
	rawAPI    *tg.Client
	uploader  *uploader.Uploader
	downer    *downloader.Downloader
	channel   *tg.InputPeerChannel
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	user      *tg.User
	isRunning bool
}

type TerminalAuth struct {
	phone string
}

func (a *TerminalAuth) Phone(ctx context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("📱 Введите номер телефона (например, +79991234567): ")
	phone, _ := reader.ReadString('\n')
	a.phone = strings.TrimSpace(phone)
	return a.phone, nil
}

func (a *TerminalAuth) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("🔑 Введите код подтверждения из Telegram: ")
	code, _ := reader.ReadString('\n')
	return strings.TrimSpace(code), nil
}

func (a *TerminalAuth) Password(ctx context.Context) (string, error) {
	fmt.Print("🔒 Введите пароль двухфакторной аутентификации (2FA): ")
	bytePassword, err := terminal.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytePassword)), nil
}

func (a *TerminalAuth) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up is not supported; please register in Telegram first")
}

func (a *TerminalAuth) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func createResolver(proxyStr string) (dcs.Resolver, error) {
	if strings.TrimSpace(proxyStr) == "" {
		return nil, nil
	}

	u, err := url.Parse(proxyStr)
	if err != nil {
		return nil, fmt.Errorf("неверный формат proxy URL: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			auth = &proxy.Auth{
				User: u.User.Username(),
			}
			if p, ok := u.User.Password(); ok {
				auth.Password = p
			}
		}
		dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("ошибка создания SOCKS5 dialer: %w", err)
		}

		return dcs.Plain(dcs.PlainOptions{
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
					return ctxDialer.DialContext(ctx, network, addr)
				}
				return dialer.Dial(network, addr)
			},
		}), nil

	case "http", "https":
		return dcs.Plain(dcs.PlainOptions{
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				conn, err := d.DialContext(ctx, "tcp", u.Host)
				if err != nil {
					return nil, err
				}

				req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
				if u.User != nil {
					userPass := u.User.String()
					authHeader := base64.StdEncoding.EncodeToString([]byte(userPass))
					req += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", authHeader)
				}
				req += "\r\n"

				if _, err := conn.Write([]byte(req)); err != nil {
					_ = conn.Close()
					return nil, err
				}

				respReader := bufio.NewReader(conn)
				resp, err := http.ReadResponse(respReader, nil)
				if err != nil {
					_ = conn.Close()
					return nil, err
				}
				if resp.StatusCode != 200 {
					_ = conn.Close()
					return nil, fmt.Errorf("HTTP proxy вернул ошибку: %s", resp.Status)
				}

				return conn, nil
			},
		}), nil

	case "tg", "mtproto":
		var host string
		var secretHex string
		if u.Scheme == "tg" {
			q := u.Query()
			server := q.Get("server")
			port := q.Get("port")
			secretHex = q.Get("secret")
			host = net.JoinHostPort(server, port)
		} else {
			host = u.Host
			if u.User != nil {
				secretHex = u.User.Username()
			}
		}

		secretBytes, err := hex.DecodeString(secretHex)
		if err != nil {
			return nil, fmt.Errorf("неверный MTProto secret hex: %w", err)
		}

		return dcs.MTProxy(host, secretBytes, dcs.MTProxyOptions{})

	default:
		return nil, fmt.Errorf("неподдерживаемый протокол прокси: %s", u.Scheme)
	}
}

func NewClient(cfg *config.Config) *Client {
	appID := cfg.ApiID
	appHash := cfg.ApiHash
	if appID == 0 || appHash == "" {
		appID = DefaultAppID
		appHash = DefaultAppHash
	}

	sessionStorage := &telegram.FileSessionStorage{
		Path: config.GetSessionPath(),
	}

	opts := telegram.Options{
		SessionStorage: sessionStorage,
	}

	if cfg.ProxyURL != "" {
		resolver, err := createResolver(cfg.ProxyURL)
		if err != nil {
			fmt.Printf("⚠️ Ошибка настройки прокси (%s): %v\n", cfg.ProxyURL, err)
		} else if resolver != nil {
			opts.Resolver = resolver
			fmt.Printf("🛡️ Прокси активирован: %s\n", cfg.ProxyURL)
		}
	}

	client := telegram.NewClient(appID, appHash, opts)

	return &Client{
		cfg:    cfg,
		client: client,
	}
}

func (c *Client) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	errChan := make(chan error, 1)
	readyChan := make(chan struct{}, 1)

	go func() {
		err := c.client.Run(c.ctx, func(ctx context.Context) error {
			c.rawAPI = c.client.API()
			c.uploader = uploader.NewUploader(c.rawAPI).WithPartSize(DefaultChunkSize)
			c.downer = downloader.NewDownloader()

			// Check auth status
			status, err := c.client.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf("failed to get auth status: %w", err)
			}

			if !status.Authorized {
				// Interactive login if needed
				flow := auth.NewFlow(
					&TerminalAuth{phone: c.cfg.PhoneNumber},
					auth.SendCodeOptions{},
				)
				if err := c.client.Auth().IfNecessary(ctx, flow); err != nil {
					return fmt.Errorf("auth flow failed: %w", err)
				}
			}

			// Get current user info
			self, err := c.client.Self(ctx)
			if err != nil {
				return fmt.Errorf("failed to get self user: %w", err)
			}
			c.user = self
			c.isRunning = true

			// Ensure cloud storage channel
			if err := c.ensureStorageChannel(ctx); err != nil {
				return fmt.Errorf("failed to prepare storage channel: %w", err)
			}

			close(readyChan)

			// Keep client running until canceled
			<-ctx.Done()
			return ctx.Err()
		})
		if err != nil && err != context.Canceled {
			errChan <- err
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-readyChan:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for Telegram connection")
	}
}

func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.isRunning = false
}

func (c *Client) IsRunning() bool {
	return c.isRunning
}

func (c *Client) GetUser() *tg.User {
	return c.user
}

func (c *Client) ensureStorageChannel(ctx context.Context) error {
	if c.cfg.StorageType == "saved_messages" {
		return nil
	}

	// Look up channel if ID is already saved
	if c.cfg.ChannelID != 0 {
		c.channel = &tg.InputPeerChannel{
			ChannelID:  c.cfg.ChannelID,
			AccessHash: 0, // GotD updates access hash automatically
		}
		return nil
	}

	// Create or find channel named "TG_Drive_Storage"
	title := "TG_Drive_Storage"
	created, err := c.rawAPI.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Broadcast: true,
		Megagroup: false,
		Title:     title,
		About:     "Virtual Cloud Drive Storage managed by tg-drive",
	})
	if err != nil {
		// Fallback to Saved Messages if channel creation is restricted
		c.cfg.StorageType = "saved_messages"
		_ = config.Save(c.cfg)
		return nil
	}

	var channelID int64
	var accessHash int64
	if updates, ok := created.(*tg.Updates); ok {
		for _, chat := range updates.Chats {
			if ch, ok := chat.(*tg.Channel); ok {
				channelID = ch.ID
				accessHash = ch.AccessHash
				break
			}
		}
	}

	if channelID != 0 {
		c.cfg.ChannelID = channelID
		c.cfg.ChannelTitle = title
		_ = config.Save(c.cfg)
		c.channel = &tg.InputPeerChannel{
			ChannelID:  channelID,
			AccessHash: accessHash,
		}
	}
	return nil
}

func (c *Client) getPeer() tg.InputPeerClass {
	if c.channel != nil && c.cfg.StorageType != "saved_messages" {
		return c.channel
	}
	return &tg.InputPeerSelf{}
}

// UploadFile performs high-performance concurrent chunked upload supporting up to 4GB files
func (c *Client) UploadFile(ctx context.Context, localPath string, progress ProgressCallback) (*UploadResult, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := stat.Size()
	fileName := filepath.Base(localPath)

	// Random 64-bit File ID
	var fileID int64
	_ = binary.Read(rand.Reader, binary.LittleEndian, &fileID)
	if fileID < 0 {
		fileID = -fileID
	}

	chunkSize := int64(DefaultChunkSize)
	totalParts := int(math.Ceil(float64(fileSize) / float64(chunkSize)))
	if totalParts == 0 {
		totalParts = 1
	}

	numWorkers := c.cfg.MaxWorkers
	if numWorkers <= 0 {
		numWorkers = 6
	}

	type partJob struct {
		partIndex int
		offset    int64
		size      int
	}

	jobs := make(chan partJob, totalParts)
	for i := 0; i < totalParts; i++ {
		offset := int64(i) * chunkSize
		partLen := int(chunkSize)
		if offset+chunkSize > fileSize {
			partLen = int(fileSize - offset)
		}
		jobs <- partJob{
			partIndex: i,
			offset:    offset,
			size:      partLen,
		}
	}
	close(jobs)

	var uploadedBytes atomic.Int64
	startTime := time.Now()

	var wg sync.WaitGroup
	errChan := make(chan error, numWorkers)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, chunkSize)

			for job := range jobs {
				select {
				case <-ctx.Done():
					errChan <- ctx.Err()
					return
				default:
				}

				// Read chunk
				n, err := file.ReadAt(buf[:job.size], job.offset)
				if err != nil && err != io.EOF {
					errChan <- fmt.Errorf("read part %d error: %w", job.partIndex, err)
					return
				}

				// Upload Big File Part via MTProto
				_, err = c.rawAPI.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{
					FileID:         fileID,
					FilePart:       job.partIndex,
					FileTotalParts: totalParts,
					Bytes:          buf[:n],
				})
				if err != nil {
					errChan <- fmt.Errorf("upload part %d error: %w", job.partIndex, err)
					return
				}

				uploaded := uploadedBytes.Add(int64(n))
				if progress != nil {
					elapsed := time.Since(startTime).Seconds()
					speed := 0.0
					if elapsed > 0 {
						speed = float64(uploaded) / elapsed
					}
					progress(uploaded, fileSize, speed)
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	// Finalize upload by sending message to Storage channel or Saved Messages
	inputFile := &tg.InputFileBig{
		ID:    fileID,
		Parts: totalParts,
		Name:  fileName,
	}

	mimeType := "application/octet-stream"
	media := &tg.InputMediaUploadedDocument{
		File:     inputFile,
		MimeType: mimeType,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{
				FileName: fileName,
			},
		},
	}

	var randomID int64
	_ = binary.Read(rand.Reader, binary.LittleEndian, &randomID)

	sentMedia, err := c.rawAPI.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     c.getPeer(),
		Media:    media,
		Message:  fmt.Sprintf("#file %s (%d bytes)", fileName, fileSize),
		RandomID: randomID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to finalize send media: %w", err)
	}

	res := &UploadResult{
		Size:     fileSize,
		MimeType: mimeType,
	}

	// Extract message ID and Document attributes
	switch u := sentMedia.(type) {
	case *tg.Updates:
		for _, update := range u.Updates {
			switch upd := update.(type) {
			case *tg.UpdateNewMessage:
				if m, ok := upd.Message.(*tg.Message); ok {
					res.MessageID = m.ID
					if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := docMedia.Document.(*tg.Document); ok {
							res.FileID = doc.ID
							res.AccessHash = doc.AccessHash
							res.FileReference = doc.FileReference
							res.MimeType = doc.MimeType
						}
					}
				}
			case *tg.UpdateNewChannelMessage:
				if m, ok := upd.Message.(*tg.Message); ok {
					res.MessageID = m.ID
					if docMedia, ok := m.Media.(*tg.MessageMediaDocument); ok {
						if doc, ok := docMedia.Document.(*tg.Document); ok {
							res.FileID = doc.ID
							res.AccessHash = doc.AccessHash
							res.FileReference = doc.FileReference
							res.MimeType = doc.MimeType
						}
					}
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		res.MessageID = u.ID
	}

	return res, nil
}

// DownloadChunk reads a specific byte slice (e.g. for streaming video/WebDAV range requests)
func (c *Client) DownloadChunk(ctx context.Context, fileID, accessHash int64, fileRef []byte, offset int64, limit int) ([]byte, error) {
	location := &tg.InputDocumentFileLocation{
		ID:            fileID,
		AccessHash:    accessHash,
		FileReference: fileRef,
	}

	res, err := c.rawAPI.UploadGetFile(ctx, &tg.UploadGetFileRequest{
		Location: location,
		Offset:   offset,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}

	if file, ok := res.(*tg.UploadFile); ok {
		return file.Bytes, nil
	}
	return nil, fmt.Errorf("unexpected file result type")
}
