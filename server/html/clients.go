package html

import (
	"encoding/gob"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/websocket"
	"math/rand"
	"os"
	"rezder.com/game/card/battleline/server/games"
	"rezder.com/game/card/battleline/server/players"
	"strconv"
	"sync"
	"time"
)

const (
	//COST the password time cost, because of future improvement in hardware.
	COST             = 5
	CLIENTS_FileName = "data/clients.gob"
)

var (
	//NAMESIZE the name character size limit.
	NAMESIZE = [2]int{4, 20}
	//PWSIZE the password size limit.
	PWSIZE = [2]int{8, 20}
)

//Client the login object. Hold information of the user including
//loged-in information.
type Client struct {
	Name    string
	Id      int
	Pw      []byte
	Disable bool
	mu      *sync.Mutex
	//Filled when logIn
	sid     string
	sidTime time.Time
	//Filled when establish websocket. Just because they login does
	// not garantie they etstablish a web socket
	ws *websocket.Conn
}

//createClient creates a new client and log the client in.
func createClient(name string, id int, pw []byte) (c *Client) {
	c = new(Client)
	c.Name = name
	c.Id = id
	c.Pw = pw
	c.sid = sessionId()
	c.sidTime = time.Now()
	c.mu = new(sync.Mutex)
	return c
}

//Clients the clients list.
type Clients struct {
	mu         *sync.RWMutex
	Clients    map[string]*Client
	NextId     int
	gameServer *games.Server
}

func NewClients(games *games.Server) (c *Clients) {
	c = new(Clients)
	c.gameServer = games
	c.mu = new(sync.RWMutex)
	c.Clients = make(map[string]*Client)
	c.NextId = 1
	return c
}

//loadClients loads client list from a file and adds
//the mutexs.
func loadClients(games *games.Server) (clients *Clients, err error) {
	file, err := os.Open(CLIENTS_FileName)
	if err == nil {
		defer file.Close()
		decoder := gob.NewDecoder(file)
		lc := *NewClients(games)
		err = decoder.Decode(&lc)
		if err == nil {
			clients = &lc
			for _, client := range clients.Clients {
				client.mu = new(sync.Mutex)
			}
		}
	} else {
		if os.IsNotExist(err) {
			err = nil
			clients = NewClients(games) //first start
		}
	}
	return clients, err

}

// save saves the client list to file.
func (clients *Clients) save() (err error) {
	file, err := os.Create(CLIENTS_FileName)
	if err == nil {
		defer file.Close()
		encoder := gob.NewEncoder(file)
		err = encoder.Encode(clients)
	}
	return err
}

//logOut logout the client. Two locks are use the map and client.
func (c *Clients) logOut(name string) {
	c.mu.RLock()
	client := c.Clients[name]
	c.mu.RUnlock()
	client.mu.Lock()
	clearLogIn(client)
	client.mu.Unlock()
}

//clearLogIn clear the login information. No locks used.
func clearLogIn(client *Client) {
	client.sid = ""
	client.sidTime = *new(time.Time)
	client.ws = nil
}

//SetGameServer set the game server. The old game server is return,
//if set to nil all http server will return game server down.
//lock is used.
func (c *Clients) SetGameServer(games *games.Server) (oldGames *games.Server) {
	oldGames = c.gameServer
	c.mu.Lock()
	c.gameServer = games
	c.mu.Unlock()
	return oldGames
}

//joinGameServer add a client to the game server.
//ok: True: if succes.
//down: True: if game server down.
//joined: True: if the client is already loged-in.
func (c *Clients) joinGameServer(name string, sid string, ws *websocket.Conn,
	errCh chan<- error, joinCh chan<- *players.Player) (ok, down, joined bool) {
	c.mu.RLock()
	if c.gameServer != nil {
		client, found := c.Clients[name]
		if found {
			client.mu.Lock()
			if client.sid != sid { //I do not think this is necessary because of the handshake
				client.mu.Unlock()
			} else {
				if client.ws == nil {
					client.ws = ws
					player := players.NewPlayer(client.Id, name, ws, errCh, joinCh)
					client.mu.Unlock()
					c.gameServer.PlayersJoinCh() <- player
					ok = true
				} else {
					joined = true
					client.mu.Unlock()
				}
			}
		}
	} else {
		down = true
	}
	c.mu.RUnlock()
	return ok, down, joined
}

// verifySid verify name and session id.
func (c *Clients) verifySid(name, sid string) (ok, down bool) {
	ok = true
	c.mu.RLock()
	down = c.gameServer == nil
	client, found := c.Clients[name]
	if found {
		client.mu.Lock()
		if !down {
			if client.sid != sid || client.ws != nil {
				ok = false
			}
		} else { //down
			ok = false
			clearLogIn(client) //Logout with out lock
		}
		client.mu.Unlock()
	}

	c.mu.RUnlock()
	return ok, down
}

//logIn log-in a client.
func (c *Clients) logIn(name string, pw string) (sid string, err error) {
	c.mu.RLock()
	if c.gameServer != nil {
		client, found := c.Clients[name]
		if found {
			client.mu.Lock()
			if !client.Disable {
				if client.sid == "" {
					err = bcrypt.CompareHashAndPassword(client.Pw, []byte(pw))
					if err == nil {
						client.sid = sessionId()
						client.sidTime = time.Now()
						sid = client.sid
					} else {
						err = errors.New("Name password combination do not exist")
					}
				} else {
					err = errors.New("Allready loged in.")
				}
			} else {
				err = errors.New("Account disabled.")
			}
			client.mu.Unlock()
		} else {
			err = errors.New("Name password combination do not exist")
		}
	} else {
		err = NewErrDown("Game server down")
	}
	c.mu.RUnlock()
	return sid, err
}

//disable disable a client.
func (c *Clients) disable(name string) {
	c.mu.RLock()
	client, found := c.Clients[name]
	c.mu.RUnlock()
	if found {
		client.mu.Lock()
		client.Disable = true
		client.mu.Unlock()
	}

}

//addNew create and log-in a new client.
// Errors: ErrExist,ErrDown,ErrSize and bcrypt errors.
func (c *Clients) addNew(name string, pwTxt string) (sid string, err error) {
	err = checkNamePwSize(name, pwTxt)
	if err == nil {
		c.mu.Lock()
		if c.gameServer != nil {
			client, found := c.Clients[name]
			if !found {
				var pwh []byte
				pwh, err = bcrypt.GenerateFromPassword([]byte(pwTxt), COST)
				if err == nil {
					client = createClient(name, c.NextId, pwh)
					c.NextId = c.NextId + 1
					c.Clients[client.Name] = client
					sid = client.sid
				}
			} else {
				err = NewErrExist("Name is used.")
			}
		} else {
			err = NewErrDown("Game server down")
		}
		c.mu.Unlock()
	}
	return sid, err
}

//create a random session id.
func sessionId() (txt string) {
	rand.Seed(time.Now().UnixNano())
	i := rand.Int()
	txt = strconv.Itoa(i)
	return txt
}

// checkNamePwSize check client name and password information for size when
//creating a new client.
func checkNamePwSize(name string, pw string) (err error) {
	nameSize := -1
	pwSize := -1

	if len(name) < NAMESIZE[0] || len(name) > NAMESIZE[1] {
		nameSize = len(name)
	}
	if len(pw) < PWSIZE[0] || len(pw) > PWSIZE[1] {
		pwSize = len(pw)
	}
	if nameSize != -1 || pwSize != -1 {
		err = NewErrSize(nameSize, pwSize)
	}

	return err
}

//ErrDown err when server is down.
type ErrDown struct {
	reason string
}

func NewErrDown(reason string) (e *ErrDown) {
	e = new(ErrDown)
	e.reason = reason
	return e
}

func (e *ErrDown) Error() string {
	return e.reason
}

//ErrSize err when password or name do meet size limits.
type ErrSize struct {
	name int
	pw   int
}

func NewErrSize(name int, pw int) (e *ErrSize) {
	e = new(ErrSize)
	e.name = name
	e.pw = pw
	return e
}
func (e *ErrSize) Error() string {
	var txt string
	switch {
	case e.name >= 0 && e.pw >= 0:
		txt = fmt.Sprintf("The lengh of Name: %v is illegal and the lenght of Password: %v is illegal.", e.name, e.pw)
	case e.name >= 0:
		txt = fmt.Sprintf("The lenght of Name: %v is illegal.", e.name)
	case e.pw >= 0:
		txt = fmt.Sprintf("The lenght of Password is illegal", e.pw)
	}
	return txt
}

// ErrExist when user name and password combination do no match or user
//do not exist.
type ErrExist struct {
	txt string
}

func NewErrExist(txt string) (e *ErrExist) {
	e = new(ErrExist)
	e.txt = txt
	return e
}
func (e *ErrExist) Error() string {
	return e.txt
}
