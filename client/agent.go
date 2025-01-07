package client

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"

	"tcp-over-http/constant"
)

type agent struct {
	conn   net.Conn
	id     string
	url    string
	target string
	key    []byte
}

func newAgent(conn net.Conn, url, target, keyStr string) *agent {
	var key []byte = nil
	if keyStr != "" {
		key = []byte(keyStr + constant.KEYPADDING)[0:32]
	}
	return &agent{
		conn:   conn,
		id:     uuid.New().String()[0:8],
		url:    url,
		target: target,
		key:    key,
	}
}

func (a *agent) work() {

	var err error
	var code byte
	ctx := context.Background()

	defer func() {
		a.conn.Close()
		log.Info().
			Str("id", a.id).
			Str("client", a.conn.RemoteAddr().String()).
			Msg("connection closed")
	}()

	//Make a connection
	code, _, err = a.proxy(ctx, constant.Establish, "", []byte(a.target))
	if err != nil {
		log.Error().Err(err).Str("id", a.id).Msg("new connection failed")
		return
	}

	if code != constant.NORMAL {
		log.Error().Err(err).Str("id", a.id).Msg("new connection failed")
		return
	}

	log.Info().Str("id", a.id).
		Str("remote", a.conn.RemoteAddr().String()).
		Msgf("new connection established")

	go a.write(ctx)

	buff := make([]byte, constant.BuffSize)
	n := 0
	for {
		n, err = a.conn.Read(buff)
		if err != nil {
			log.Error().Err(err).Str("id", a.id).Msg("client local read failed")
			_, _, _ = a.proxy(ctx, constant.Goodbye, "", nil)
			_ = a.conn.Close()
			return
		}

		if n > 0 {
			code, _, err = a.proxy(ctx, constant.Write, uuid.New().String()[0:8], buff[0:n])

			if err != nil {
				log.Error().Err(err).Str("id", a.id).Msg("client proxy send failed")
				_, _, _ = a.proxy(ctx, constant.Goodbye, "", nil)
				_ = a.conn.Close()
				return
			}

			if code == constant.ERROR {
				log.Error().Str("id", a.id).Msg("received server error")
				_, _, _ = a.proxy(ctx, constant.Goodbye, "", nil)
				_ = a.conn.Close()
				return
			}
		}
	}
}

func (a *agent) write(ctx context.Context) {
	for {
		code, raw, err := a.proxy(ctx, constant.Read, uuid.New().String()[0:8], nil)
		if err != nil {
			log.Error().Err(err).Str("id", a.id).Msg("client proxy receive failed")
			_ = a.conn.Close()
			return
		}

		if code == constant.ERROR {
			log.Error().Str("id", a.id).Msg("received server error")
			_ = a.conn.Close()
			return
		}

		// log.Debug().Str("id", a.id).Msgf("received %d bytes", len(raw))

		if len(raw) > 0 {
			_, err = a.conn.Write(raw)

			if err != nil {
				log.Error().Err(err).Str("id", a.id).Msg("client local write failed")
				_ = a.conn.Close()
				return
			}
		}

		if code == constant.EOF {
			log.Error().Str("id", a.id).Msg("received server eof")
			_ = a.conn.Close()
			return
		}
	}
}

func (a *agent) proxy(ctx context.Context, action string, itemId string, data []byte) (byte, []byte, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, time.Minute*6)
	defer cancel()

	log.Debug().Str("id", a.id).Str("itemId", itemId).Msgf("send %d bytes", len(data))

	var reader io.Reader
	if len(data) > 0 {
		if a.key != nil {
			var err error
			data, err = encrypt(data, a.key)
			if err != nil {
				return constant.ERROR, nil, fmt.Errorf("new http request: %w", err)
			}
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodPost, a.url, reader)
	if err != nil {
		return constant.ERROR, nil, fmt.Errorf("new http request: %w", err)
	}

	req.Header.Set("Proxy-Id", a.id)
	// req.Header.Set("Proxy-Target", a.target)
	req.Header.Set("Proxy-Action", action)
	req.Header.Set("Proxy-ItemId", itemId)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {

		if itemId != "" {
			time.Sleep(time.Second * 3)
			log.Info().Str("id", a.id).Str("itemId", itemId).Msgf("resend %d bytes", len(data))
			req, err = http.NewRequestWithContext(ctxTimeout, http.MethodPost, a.url, reader)
			if err != nil {
				return constant.ERROR, nil, fmt.Errorf("new http request: %w", err)
			}

			req.Header.Set("Proxy-Id", a.id)
			// req.Header.Set("Proxy-Target", a.target)
			req.Header.Set("Proxy-Action", action)
			req.Header.Set("Proxy-ItemId", itemId)
			resp, err = http.DefaultClient.Do(req)
		}

		if err != nil {
			return constant.ERROR, nil, fmt.Errorf("do http request: %w", err)
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return constant.ERROR, nil, fmt.Errorf("do http request: status code %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return constant.ERROR, nil, fmt.Errorf("read response body: %w", err)
	}

	code := constant.ERROR
	if len(raw) > 0 {
		code = raw[0]
		log.Debug().Str("id", a.id).Str("itemId", itemId).Msgf("receive code %d", int(code))
		if len(raw) > 1 {
			raw = raw[1:]
			if a.key != nil {
				raw, err = decrypt(raw, a.key)
				if err != nil {
					return constant.ERROR, nil, fmt.Errorf("read response body: %w", err)
				}
			}
		} else {
			raw = nil
		}
	}

	return code, raw, nil
}

func encrypt(plaintext []byte, key []byte) (encryptedText []byte, err error) {

	//Create a new Cipher Block from the key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	//Create a new GCM - https://en.wikipedia.org/wiki/Galois/Counter_Mode
	//https://golang.org/pkg/crypto/cipher/#NewGCM
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	//Create a nonce. Nonce should be from GCM
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	//Encrypt the data using aesGCM.Seal
	//Since we don't want to save the nonce somewhere else in this case, we add it as a prefix to the encrypted data. The first nonce argument in Seal is the prefix.
	ciphertext := aesGCM.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

func decrypt(enc []byte, key []byte) (plaintext []byte, err error) {

	//Create a new Cipher Block from the key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	//Create a new GCM
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	//Get the nonce size
	nonceSize := aesGCM.NonceSize()

	//Extract the nonce from the encrypted data
	nonce, ciphertext := enc[:nonceSize], enc[nonceSize:]

	//Decrypt the data
	plaintext, err = aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}
