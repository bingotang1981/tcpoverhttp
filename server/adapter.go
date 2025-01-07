package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"tcp-over-http/constant"
)

var (
	adapters = make(map[string]*adapter)
	mutex    sync.RWMutex
)

// var key = []byte("012345678901234567890123456789ab")

type adapter struct {
	id       string
	target   string
	conn     net.Conn
	reqId    string
	respId   string
	respData []byte
	key      []byte
	alive    bool
	t        int64
}

func getAdapter(id string) *adapter {
	mutex.RLock()
	defer mutex.RUnlock()
	return adapters[id]
}

func deleteAdapter(id string) {
	mutex.Lock()
	defer mutex.Unlock()

	if ad := adapters[id]; ad != nil {
		ad.alive = false
		ad.shutdown()
		delete(adapters, id)
	}
}

func monitorConnHeartbeat() {
	mutex.Lock()
	defer mutex.Unlock()

	startTime := time.Now().Unix()
	nowTimeCut := startTime - constant.HEART_BEAT_INTERVAL

	for _, ad := range adapters {
		if ad != nil && ad.t < nowTimeCut {
			ad.alive = false
			ad.shutdown()
			delete(adapters, ad.id)
		}
	}

	endTime := time.Now().Unix()

	log.Info().Msgf("Active adapters %d, (%ds)", len(adapters), endTime-startTime)
}

func newAdapter(id string, rawTarget []byte, keyStr string) (*adapter, error) {
	var key []byte = nil
	if keyStr != "" {
		key = []byte(keyStr + constant.KEYPADDING)[0:32]
	}

	if key != nil {
		var err error = nil
		rawTarget, err = decrypt(rawTarget, key)

		if err != nil {
			return nil, err
		}
	}

	target := string(rawTarget)

	conn, err := net.DialTimeout("tcp", target, time.Second*5)
	if err != nil {
		return nil, fmt.Errorf("dail %s: %w", target, err)
	}

	mutex.Lock()
	defer mutex.Unlock()

	ad := &adapter{
		id:     id,
		target: target,
		conn:   conn,
		key:    key,
		alive:  true,
	}

	adapters[id] = ad
	return ad, nil
}

func getRawTarget(c *gin.Context) ([]byte, error) {
	return io.ReadAll(c.Request.Body)
}

func (a *adapter) write(c *gin.Context, itemId string) {

	var data []byte = []byte{constant.NORMAL}

	if itemId != "" {
		if itemId == a.reqId {
			log.Debug().Str("id", a.id).Msgf("Duplicated aId %s bytes", itemId)
			data[0] = constant.DUP_ITEM_ID
			c.Data(http.StatusOK, "application/octet-stream", data)
			return
		}
	}

	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Error().Err(err).Str("id", a.id).Msgf("write failed")
		data[0] = constant.ERROR
		c.Data(http.StatusOK, "application/octet-stream", data)
		return
	}

	log.Debug().Str("id", a.id).Str("aId", itemId).Msgf("received %d bytes", len(raw))

	if len(raw) == 0 {
		a.reqId = itemId
		c.Data(http.StatusOK, "application/octet-stream", data)
		return
	}

	if a.key != nil {
		raw, err = decrypt(raw, a.key)

		if err != nil {
			data[0] = constant.ERROR
			c.Data(http.StatusOK, "application/octet-stream", data)
			return
		}
	}

	_, err = a.conn.Write(raw)
	if err != nil {
		log.Error().Err(err).Str("id", a.id).Msgf("write failed")
		data[0] = constant.ERROR
		c.Data(http.StatusOK, "application/octet-stream", data)
	} else {
		a.reqId = itemId
		c.Data(http.StatusOK, "application/octet-stream", data)
	}

}

func (a *adapter) read(c *gin.Context, itemId string) {
	//set the access time
	a.t = time.Now().Unix()

	var data []byte = []byte{constant.NORMAL}

	if itemId != "" {
		if itemId == a.respId {
			c.Data(http.StatusOK, "application/octet-stream", a.respData)
		}
	}
	var err error
	var n int

	buff := make([]byte, constant.BuffSize)

	for i := 0; i < 10; i++ {
		dur := time.Millisecond * 100
		if i == 0 {
			dur = time.Second * 30
		}

		_ = a.conn.SetReadDeadline(time.Now().Add(dur))
		n, err = a.conn.Read(buff)
		if err == nil {
			data = append(data, buff[0:n]...)
		} else {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				data[0] = constant.TIMEOUT
				break
			} else if errors.Is(err, io.EOF) {
				data[0] = constant.EOF
				break
			} else {
				if a.alive {
					log.Error().Err(err).Str("id", a.id).Msgf("read failed")
				}
				data[0] = constant.ERROR
				break
			}
		}
	}

	if len(data) > 0 {
		if len(data) > 1 {
			if a.key != nil {
				enc, err := encrypt(data[1:], a.key)
				data = data[0:1]
				if err != nil {
					data[0] = constant.ERROR
				} else {
					data = append(data, enc...)
				}
			}
		}

		if itemId != "" {
			a.respId = itemId
			a.respData = data
		}
		c.Data(http.StatusOK, "application/octet-stream", data)
		log.Debug().Str("id", a.id).Str("bId", itemId).Msgf("send %d bytes with code as %d", len(data), int(data[0]))
	}
}

func (a *adapter) shutdown() {
	_ = a.conn.Close()
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
