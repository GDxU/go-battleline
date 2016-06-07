package players

import (
	"fmt"
	"golang.org/x/net/websocket"
	"io"
	pub "rezder.com/game/card/battleline/server/publist"
	"rezder.com/game/card/battleline/server/tables"
	"strconv"
)

const (
	ACT_MESS       = 1
	ACT_INVITE     = 2
	ACT_INVACCEPT  = 3
	ACT_INVDECLINE = 4
	ACT_INVRETRACT = 5
	ACT_MOVE       = 6
	ACT_QUIT       = 7
	ACT_WATCH      = 8
	ACT_WATCHSTOP  = 9
	ACT_LIST       = 10

	JT_Mess      = 1
	JT_Invite    = 2
	JT_Move      = 3
	JT_BenchMove = 4
	JT_List      = 5
	JT_CloseCon  = 6

	WRTBUFF_SIZE = 10
	WRTBUFF_LIM  = 8
)

// Start start the players server.
func Start(joinCh <-chan *Player, pubList *pub.List, startGameChCl *tables.StartGameChCl,
	finishedCh chan struct{}) {
	leaveCh := make(chan int)
	list := make(map[int]*pub.PlayerData)
	done := false
Loop:
	for {
		select {
		case id := <-leaveCh:
			delete(list, id)
			if done && len(list) == 0 {
				break Loop
			}
			publish(list, pubList)
		case p, open := <-joinCh:
			if open {
				inviteCh := make(chan *pub.Invite)
				messCh := make(chan *pub.MesData)
				p.joinServer(inviteCh, messCh, leaveCh, pubList, startGameChCl)
				p.joinedCh <- p
				list[p.id] = &pub.PlayerData{p.id, p.name, inviteCh, p.doneComCh, messCh, p.bootCh}
				publish(list, pubList)
			} else {
				if len(list) > 0 {
					for _, player := range list {
						close(player.BootCh)
					}
				} else {
					break Loop
				}
			}
		}
	}
	close(finishedCh)
}

// publish publish the players map.
func publish(list map[int]*pub.PlayerData, pubList *pub.List) {
	copy := make(map[int]*pub.PlayerData)
	for key, v := range list {
		copy[key] = v
	}
	go pubList.UpdatePlayers(copy)
}

// Player it is the player information used to exchange between the logon server and
// the players server.
type Player struct {
	id          int
	name        string
	tableStChCl *tables.StartGameChCl
	leaveCh     chan<- int
	pubList     *pub.List
	inviteCh    <-chan *pub.Invite
	doneComCh   chan struct{}
	messCh      <-chan *pub.MesData
	ws          *websocket.Conn
	errCh       chan<- error
	bootCh      chan struct{} //server boot channel. used to kick player out.
	joinedCh    chan<- *Player
}

func NewPlayer(id int, name string, ws *websocket.Conn, errCh chan<- error,
	joinCh chan<- *Player) (p *Player) {
	p = new(Player)
	p.id = id
	p.name = name
	p.ws = ws
	p.doneComCh = make(chan struct{})
	p.errCh = errCh
	p.bootCh = make(chan struct{})
	p.joinedCh = joinCh
	return p
}

// joinServer add the players server information.
func (p *Player) joinServer(inviteCh <-chan *pub.Invite, messCh <-chan *pub.MesData,
	leaveCh chan<- int, pubList *pub.List, startGameChCl *tables.StartGameChCl) {
	p.leaveCh = leaveCh
	p.pubList = pubList
	p.inviteCh = inviteCh
	p.messCh = messCh
	p.tableStChCl = startGameChCl
}

// Start a player server.
// The server will not wait for its started go routine to finsh but it will send
// the kill signal to them.
func (player *Player) Start() {
	sendCh := make(chan interface{}, WRTBUFF_SIZE)
	wrtBrookCh := make(chan struct{})
	readList := player.pubList.Read()
	go netWrite(player.ws, sendCh, player.errCh, wrtBrookCh)

	c := make(chan *Action, 1)
	go netRead(player.ws, c, player.doneComCh, player.errCh)
	var actChan <-chan *Action
	actChan = c

	inviteResponse := make(chan *pub.InviteResponse)

	sendInvites := make(map[int]*pub.Invite)
	receivedInvites := make(map[int]*pub.Invite)

	gameReceiveCh := make(chan *pub.MoveView) //if already sat game is finished and
	//gameChan should be sat to nil
	var gameRespCh chan<- [2]int
	var gameMove *pub.MoveView

	watchGameCh := make(chan *startWatchGameData)
	var watchGames map[int]*pub.WatchChCl
	sendCh <- readList
	sendSysMess(sendCh, "Welcome to Battleline!\n Will you play a game?")
Loop:
	for {
		select {
		case <-player.bootCh:
			handleCloseDown(player.doneComCh, gameRespCh, watchGames, player.id,
				receivedInvites, sendInvites, sendCh)
			break Loop
		case <-wrtBrookCh:
			handleCloseDown(player.doneComCh, gameRespCh, watchGames, player.id,
				receivedInvites, sendInvites, sendCh)
			break Loop
		case act, open := <-actChan:
			if open {
				upd := false
				switch act.ActType {
				case ACT_MESS:
					upd = actMessage(act, readList, sendCh, player.id, player.name)
				case ACT_INVITE:
					upd = actSendInvite(sendInvites, inviteResponse, player.doneComCh,
						act, readList, sendCh, player.id, player.name)
				case ACT_INVACCEPT:
					upd = actAccInvite(receivedInvites, act, sendCh, gameReceiveCh, player.doneComCh, player.id)
				case ACT_INVDECLINE:
					upd = actDeclineInvite(receivedInvites, act, player.id)
				case ACT_INVRETRACT:
					actRetractInvite(sendInvites, act)
				case ACT_MOVE:
					actMove(act, gameRespCh, gameMove, sendCh)
				case ACT_QUIT:
					if gameRespCh != nil {
						close(gameRespCh)
					} else {
						sendSysMess(sendCh, "Quitting game with no game active")
					}
				case ACT_WATCH:
					upd = actWatch(watchGames, act, watchGameCh, player.doneComCh, readList,
						sendCh, player.id)
				case ACT_WATCHSTOP:
					actWatchStop(watchGames, act, sendCh, player.id)
				case ACT_LIST:
					upd = true
				default:
					sendSysMess(sendCh, "No existen action")
				}
				if upd {
					readList = player.pubList.Read()
					sendCh <- readList
				}
			} else {
				handleCloseDown(player.doneComCh, gameRespCh, watchGames, player.id,
					receivedInvites, sendInvites, sendCh)
				break Loop
			}

		case move := <-gameReceiveCh:
			gameRespCh, gameMove, readList = handleGameReceive(move, sendCh, player.pubList, readList, gameRespCh,
				receivedInvites, sendInvites, player.id)
		case wgd := <-watchGameCh:
			if wgd.chCl == nil { //set chan to nil for done
				delete(watchGames, wgd.player)
			} else {
				watchGames[wgd.player] = wgd.chCl
				sendCh <- wgd.initMove
			}
		case invite := <-player.inviteCh:
			readList = playerExist(player.pubList, readList, invite.InvitorId, sendCh)
			_, found := readList[strconv.Itoa(invite.InvitorId)]
			if found {
				receivedInvites[invite.InvitorId] = invite
				sendCh <- invite
			}

		case response := <-inviteResponse:
			invite, found := sendInvites[response.Responder]
			if found {
				handleInviteResponse(response, invite, sendInvites, sendCh, player.id, player.doneComCh,
					gameReceiveCh, player.tableStChCl)
			} else {
				if response.GameCh != nil {
					close(response.GameCh)
				}
			}
		case message := <-player.messCh:
			readList = playerExist(player.pubList, readList, message.Sender, sendCh)
			_, found := readList[strconv.Itoa(message.Sender)]
			if found {
				sendCh <- message
			}
		}
	}
	player.leaveCh <- player.id
}

//actRetractInvite retract an invite
func actRetractInvite(sendInvites map[int]*pub.Invite, act *Action) {
	invite, found := sendInvites[act.Id]
	if found {
		close(invite.Retract)
		delete(sendInvites, act.Id)
	}
}

//handleInviteResponse handle a invite response.
//# sendInvites
func handleInviteResponse(response *pub.InviteResponse, invite *pub.Invite,
	sendInvites map[int]*pub.Invite, sendCh chan<- interface{}, playerId int, doneComCh chan struct{},
	gameReceiveCh chan<- *pub.MoveView, tableStChCl *tables.StartGameChCl) {

	delete(sendInvites, response.Responder)
	if response.GameCh == nil {
		invite.Rejected = true
		sendCh <- invite
	} else {
		moveRecCh := make(chan *pub.MoveView, 1)
		tableData := new(tables.StartGameData)
		tableData.PlayerIds = [2]int{playerId, response.Responder}
		tableData.PlayerChs = [2]chan<- *pub.MoveView{moveRecCh, response.GameCh}
		select {
		case tableStChCl.Channel <- tableData:
			go gameListen(gameReceiveCh, doneComCh, moveRecCh, sendCh, invite)
		case <-tableStChCl.Close:
			txt := fmt.Sprintf("Failed to start game with %v as server no longer accept games",
				response.Name)
			sendSysMess(sendCh, txt)
		}
	}
}

//handleGameReceive handles the recieve move from the game server.
// #receivedInvites
// #sendInvites
func handleGameReceive(move *pub.MoveView, sendCh chan<- interface{}, pubList *pub.List,
	readList map[string]*pub.Data, gameRespCh chan<- [2]int, receivedInvites map[int]*pub.Invite,
	sendInvites map[int]*pub.Invite, playerId int) (updGameRespCh chan<- [2]int,
	gameMove *pub.MoveView, updReadList map[string]*pub.Data) {
	updReadList = readList
	updGameRespCh = gameRespCh
	if move == nil {
		gameRespCh = nil
		gameMove = nil
	} else {
		if gameRespCh == nil { //Init move
			gameRespCh = move.MoveCh
			gameMove = move
			clearInvites(receivedInvites, sendInvites, playerId)
			sendCh <- move
			updReadList = pubList.Read()
			sendCh <- updReadList
		} else {
			gameMove = move
			sendCh <- move
		}
	}
	return updGameRespCh, gameMove, updReadList
}

// handleCloseDown close down the player.
//# receivedInvites
//# sendInvites
func handleCloseDown(doneComCh chan struct{}, gameRespCh chan<- [2]int,
	watchGames map[int]*pub.WatchChCl, playerId int, receivedInvites map[int]*pub.Invite,
	sendInvites map[int]*pub.Invite, sendCh chan<- interface{}) {
	close(doneComCh)
	if gameRespCh != nil {
		close(gameRespCh)
	}
	if len(watchGames) > 0 {
		for _, ch := range watchGames {
			stopWatch(ch, playerId)
		}
	}
	clearInvites(receivedInvites, sendInvites, playerId)
	sendCh <- CloseCon("Server close the connection")
}

//actWatch handle client action watch a game.
//# watchGames
func actWatch(watchGames map[int]*pub.WatchChCl, act *Action,
	startWatchGameCh chan<- *startWatchGameData, gameDoneCh chan struct{}, readList map[string]*pub.Data,
	sendCh chan<- interface{}, playerId int) (updList bool) {
	_, found := watchGames[act.Id] // This test only works for game started, the bench server should reject
	// any repeat request to start game.
	if found {
		txt := fmt.Sprintf("Start watching id: %v faild as you are already watching", act.Id)
		sendSysMess(sendCh, txt)
	} else {
		watch := new(pub.WatchData)
		benchCh := make(chan *pub.MoveBench, 1)
		watch.Id = playerId
		watch.Send = benchCh
		watchChCl := pub.NewWatchChCl()
		select {
		case watchChCl.Channel <- watch:
			go watchGameListen(watchChCl, benchCh, startWatchGameCh, gameDoneCh, sendCh, act.Id, playerId)
		case <-watchChCl.Close:
			txt := fmt.Sprintf("Player id: %v do not have a active game")
			sendSysMess(sendCh, txt)
			updList = true
		}
	}
	return updList
}

// watchGameListen handle game information from a watch game.
// If doneCh is closed it will close the game connection in init stage, and
// in normal state it will stop resending but keep reading until connection is closed.
func watchGameListen(watchChCl *pub.WatchChCl, benchCh <-chan *pub.MoveBench,
	startWatchGameCh chan<- *startWatchGameData, doneCh chan struct{}, sendCh chan<- interface{},
	watchId int, playerId int) {

	initMove, initOpen := <-benchCh
	if initOpen {
		stData := new(startWatchGameData)
		stData.player = watchId
		stData.initMove = initMove
		stData.chCl = watchChCl
		select {
		case startWatchGameCh <- stData:
		case <-doneCh:
			stopWatch(watchChCl, playerId)
		}
		writeStop := false
	Loop:
		for {
			move, open := <-benchCh
			if !writeStop {
				if open {
					select {
					case <-doneCh:
						writeStop = true
					default:
						select {
						case sendCh <- move:
						case <-doneCh:
							writeStop = true
						}
					}
				} else { //Game is finished.
					stData = new(startWatchGameData)
					stData.player = watchId
					select {
					case startWatchGameCh <- stData:
					case <-doneCh:
					}
					break Loop
				}
			}
		}
	} else {
		txt := fmt.Sprintf("Failed to start watching game with player id %v\n.There could be two reason for this one your already started watching or the game is finished.", watchId)
		sendSysMessGo(sendCh, doneCh, txt) //TODO request a update list here
	}

}

// actWatchStop stop watching a game.
func actWatchStop(watchGames map[int]*pub.WatchChCl, act *Action,
	sendCh chan<- interface{}, playerId int) {
	watchChCl, found := watchGames[act.Id]
	if found {
		stopWatch(watchChCl, playerId)
		delete(watchGames, act.Id)
	} else {
		txt := fmt.Sprintf("Stop watching player %v failed", act.Id)
		sendSysMess(sendCh, txt)
	}
}

// stopWatch sends the stop watch signal if possibel.
func stopWatch(watchChCl *pub.WatchChCl, playerId int) {
	watch := new(pub.WatchData)
	watch.Id = playerId
	watch.Send = nil //stop
	select {
	case watchChCl.Channel <- watch:
	case <-watchChCl.Close:
	}
}

// actMove makes a game move if the move is valid.
func actMove(act *Action, gameRespCh chan<- [2]int, gameMove *pub.MoveView,
	sendCh chan<- interface{}) {
	if gameRespCh != nil && gameMove != nil {
		valid := false
		if gameMove.MovesPass {
			if act.Move[0] == -1 && act.Move[1] == -1 {
				valid = true
			}
		} else if gameMove.MovesHand != nil {
			handmoves, found := gameMove.MovesHand[strconv.Itoa(act.Move[0])]
			if found && act.Move[1] >= 0 && act.Move[1] < len(handmoves) {
				valid = true
			}
		} else if gameMove.Moves != nil {
			if act.Move[1] >= 0 && act.Move[1] < len(gameMove.Moves) {
				valid = true
			}
		}
		if valid {
			gameRespCh <- act.Move
		} else {
			txt := "Illegal Move"
			sendSysMess(sendCh, txt) //TODO All action error should be loged also
			//as it could be sign of hacking and it should not be possible to crash!!!
		}
	} else {
		txt := "Making a move with out an active game"
		sendSysMess(sendCh, txt)
	}
}

// actAccInvite accept a invite.
func actAccInvite(recInvites map[int]*pub.Invite, act *Action, sendCh chan<- interface{},
	startGame chan<- *pub.MoveView, doneCh chan struct{}, id int) (upd bool) {
	invite, found := recInvites[act.Id]
	if found {
		resp := new(pub.InviteResponse)
		resp.Responder = id
		moveRecCh := make(chan *pub.MoveView, 1)
		resp.GameCh = moveRecCh
		select {
		case <-invite.Retract:
			txt := fmt.Sprintf("Accepting invite from %v failed invitation retracted", invite.InvitorName)
			sendSysMess(sendCh, txt)
		default:
			select {
			case invite.Response <- resp:
				go gameListen(startGame, doneCh, moveRecCh, sendCh, invite)
			case <-invite.DoneComCh:
				txt := fmt.Sprintf("Accepting invite from %v failed player done", invite.InvitorName)
				sendSysMess(sendCh, txt)
				upd = true
			}
		}
		delete(recInvites, invite.InvitorId)
	} else {
		txt := fmt.Sprintf("Invite id %v do not exist.", act.Id)
		sendSysMess(sendCh, txt)
	}
	return upd
}

// gameListen listen for game moves.
// If doneCh is closed and init state the game response channel is closed else
// the listener keep listening until the channel is close but it do not resend the moves.
//
func gameListen(gameCh chan<- *pub.MoveView, doneCh chan struct{}, moveRecCh <-chan *pub.MoveView,
	sendCh chan<- interface{}, invite *pub.Invite) {
	initMove, initOpen := <-moveRecCh
	if initOpen {
		select {
		case gameCh <- initMove:
		case <-doneCh:
			close(initMove.MoveCh)
		}
		var writeStop bool
	Loop:
		for {
			move, open := <-moveRecCh
			if !writeStop {
				if open {
					if move.MyTurn {
						select {
						case gameCh <- move:
						case <-doneCh:
							writeStop = true
						}
					} else {
						select {
						case <-doneCh: //sendCh is buffered and closed at a later stage
							writeStop = true
						default:
							select {
							case sendCh <- move:
							case <-doneCh:
								writeStop = true
							}
						}
					}
				} else {
					select {
					case gameCh <- nil:
					case <-doneCh:
					}
					break Loop
				}
			}
		}
	} else {
		invite.Rejected = true
		sendCh <- invite
	}
}

//actSendInvite send a invite.
func actSendInvite(invites map[int]*pub.Invite, respCh chan<- *pub.InviteResponse, doneCh chan struct{},
	act *Action, readList map[string]*pub.Data, sendCh chan<- interface{}, id int, name string) (upd bool) {
	p, found := readList[strconv.Itoa(act.Id)]
	if found {
		invite := new(pub.Invite)
		invite.InvitorId = id
		invite.InvitorName = name
		invite.ReceiverId = p.Id
		invite.Response = respCh
		invite.Retract = make(chan struct{})
		invite.DoneComCh = doneCh
		select {
		case p.Invite <- invite:
			invites[p.Id] = invite
		case <-p.DoneCom:
			m := fmt.Sprintf("Invite to %v failed player done", p.Name)
			sendSysMess(sendCh, m)
			upd = true
		}
	} else {
		m := fmt.Sprintf("Invite to Id: %v failed could not find player", act.Id)
		sendSysMess(sendCh, m)
		upd = true
	}
	return upd
}

// actMessage send a message.
func actMessage(act *Action, readList map[string]*pub.Data, sendCh chan<- interface{},
	id int, name string) (upd bool) {
	p, found := readList[strconv.Itoa(act.Id)]
	if found {
		mess := new(pub.MesData)
		mess.Sender = id
		mess.Name = name
		mess.Message = act.Mess
		select {
		case p.Message <- mess:
		case <-p.DoneCom:
			m := fmt.Sprintf("Message to Id: %v failed", act.Id)
			sendSysMess(sendCh, m)
			upd = true
		}
	} else {
		m := fmt.Sprintf("Message to Id: %v failed", act.Id)
		sendSysMess(sendCh, m)
		upd = true
	}
	return upd
}

// actDeclineInvite decline a invite.
func actDeclineInvite(receivedInvites map[int]*pub.Invite, act *Action, playerId int) (upd bool) {
	invite, found := receivedInvites[act.Id]
	if found {
		upd = declineInvite(invite, playerId)
		delete(receivedInvites, invite.InvitorId)
	}
	return upd
}

// declineInvite send the decline signal to opponent.
func declineInvite(invite *pub.Invite, playerId int) (upd bool) {
	resp := new(pub.InviteResponse)
	resp.Responder = playerId
	resp.GameCh = nil
	select {
	case <-invite.Retract:
	default:
		select {
		case invite.Response <- resp:
		case <-invite.DoneComCh:
			upd = true
		}
	}
	return upd
}

// playerExist check if a player exist in the public list if not update the list.
func playerExist(pubList *pub.List, readList map[string]*pub.Data, id int,
	sendCh chan<- interface{}) (upd map[string]*pub.Data) {
	_, found := readList[strconv.Itoa(id)]
	if !found {
		upd = pubList.Read()
		sendCh <- upd
	} else {
		upd = readList
	}
	return upd
}

// sendSysMess send a system message, there send channel is not check for closed,
// it is assumed to be open. Use go instead if not sure.
func sendSysMess(ch chan<- interface{}, txt string) {
	mess := new(pub.MesData)
	mess.Sender = -1
	mess.Name = "System"
	mess.Message = txt
	ch <- mess
}

// sendSysMessGo send a system message if possible.
func sendSysMessGo(sendCh chan<- interface{}, doneCh chan struct{}, txt string) {
	mess := new(pub.MesData)
	mess.Sender = -1
	mess.Name = "System"
	mess.Message = txt
	select {
	case sendCh <- mess:
	case <-doneCh:
	}
}

// clearInvites clear invites. Retract all send and cancel all recieved.
func clearInvites(receivedInvites map[int]*pub.Invite, sendInvites map[int]*pub.Invite,
	playerId int) {
	if len(receivedInvites) != 0 {
		resp := new(pub.InviteResponse)
		resp.Responder = playerId
		resp.GameCh = nil
		for _, invite := range receivedInvites {
			select {
			case invite.Response <- resp:
			case <-invite.Retract:
			}
		}
		for id, _ := range receivedInvites {
			delete(receivedInvites, id)
		}
	}
	if len(sendInvites) != 0 {
		for _, invite := range sendInvites {
			close(invite.Retract)
		}
		for id, _ := range sendInvites {
			delete(receivedInvites, id)
		}
	}
}

// CloseCon close connection signal to netWrite.
type CloseCon string

// netWrite keep reading until overflow/broken line or done message is send.
// overflow/broken line disable the write to net but keeps draining the pipe.
func netWrite(ws *websocket.Conn, dataCh <-chan interface{}, errCh chan<- error, brokenConn chan struct{}) {
	broke := false
	stop := false
Loop:
	for {
		data := <-dataCh
		_, stop = data.(CloseCon)
		if !broke {
			err := websocket.JSON.Send(ws, netWrite_AddJsonType(data))
			if err != nil {
				errCh <- err
				broke = true
			} else {
				if len(dataCh) > WRTBUFF_LIM {
					broke = true
				}
			}
			if broke {
				close(brokenConn)
			}
		}
		if stop {
			break Loop
		}
	}
}
func netWrite_AddJsonType(data interface{}) (jdata *pub.JsonData) {
	jdata = new(pub.JsonData)
	jdata.Data = data
	switch data.(type) {
	case map[string]*pub.Data:
		jdata.JsonType = JT_List
	case *pub.Invite:
		jdata.JsonType = JT_Invite
	case *pub.MesData:
		jdata.JsonType = JT_Mess
	case *pub.MoveView:
		jdata.JsonType = JT_Move
	case *pub.MoveBench:
		jdata.JsonType = JT_BenchMove
	case CloseCon:
		jdata.JsonType = JT_CloseCon
	default:
		fmt.Printf("Message not implemented yet: %v\n", data)

	}
	return jdata
}

//netRead reading data from a websocket.
//Keep reading until eof,an error or done. Done can not breake the read stream
//so to make sure to end loop websocket must be closed.
//It always close the channel before leaving.
func netRead(ws *websocket.Conn, accCh chan<- *Action, doneCh chan struct{}, errCh chan<- error) {

Loop:
	for {
		var act Action
		err := websocket.JSON.Receive(ws, &act)
		if err == io.EOF {

			break Loop
		} else if err != nil {
			errCh <- err
			break Loop //maybe to harsh
		} else {
			select {
			case accCh <- &act:
			case <-doneCh:
				break Loop
			}
		}
	}
	close(accCh)
}

// Action the client action.
type Action struct {
	ActType int
	Id      int
	Move    [2]int
	Mess    string
}

func NewAction() (a *Action) {
	a = new(Action)
	a.Id = -1 //TODO id do not thing it is necessary as player start from 1 now
	a.Move = [2]int{-1, -1}
	return a
}

// startWatchGameData the data send on startWatchGameCh
// to inform player server about starting and stoping to watch a game.
type startWatchGameData struct {
	chCl     *pub.WatchChCl
	initMove *pub.MoveBench
	player   int
}
