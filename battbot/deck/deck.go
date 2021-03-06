package deck

import (
	bat "github.com/rezder/go-battleline/battleline"
	"github.com/rezder/go-battleline/battleline/cards"
	slice "github.com/rezder/go-slice/int"
)

//Deck contain all information of unknown cards.
//That includes decks and the hand of the opponent.
//It also tracks cards returned to deck when scout is played.
type Deck struct {
	troops            map[int]bool
	tacs              map[int]bool
	scoutReturnTroops []int //contains the returned cards until drawn
	scoutReturnTacs   []int //contains the returned cards until drawn
	oppHand           []int //contains opponent scout return cards until played.
	oppTroops         int
	oppTacs           int
	tfSRMTroops       []int //contains the returned cards
	tfSRMTacs         []int //contains the returned cards
	oppKnowsCardixs   []int //The cards that the bot have drawn that the opponent have returned
	oppKnowTroopsNo   int   //The number of cards the oppent knows in deck
	oppKnowTacsNo     int   //The number of cards the oppent knows in deck
}

//NewDeck creates a new deck.
func NewDeck() (deck *Deck) {
	deck = new(Deck)
	deck.oppTroops = bat.NOHandInit
	deck.troops = make(map[int]bool)
	deck.tacs = make(map[int]bool)
	initDecks(deck.troops, deck.tacs)
	deck.scoutReturnTacs = make([]int, 0, 2)
	deck.scoutReturnTroops = make([]int, 0, 2)
	deck.oppHand = make([]int, 0, 2)
	deck.tfSRMTacs = make([]int, 0, 2)
	deck.tfSRMTroops = make([]int, 0, 2)
	deck.oppKnowsCardixs = make([]int, 0, 2)
	return deck
}

//Troops returns a all troops in the deck.
func (deck *Deck) Troops() map[int]bool {
	troops := make(map[int]bool)
	for troop := range deck.troops {
		troops[troop] = true
	}
	for _, troop := range deck.scoutReturnTroops {
		troops[troop] = true
	}
	return troops
}

//Tacs returns a all tactic cards in the deck.
func (deck *Deck) Tacs() map[int]bool {
	tacs := make(map[int]bool)
	for tactic := range deck.tacs {
		tacs[tactic] = true
	}
	for _, tactic := range deck.scoutReturnTacs {
		tacs[tactic] = true
	}
	return tacs
}

//OppHand returns the opponent hand it is copy.
func (deck *Deck) OppHand() []int {
	hand := make([]int, len(deck.oppHand))
	copy(hand, deck.oppHand)
	return hand
}

//OppDrawNo calculate the opponent number of unknown cards.
func (deck *Deck) OppDrawNo(isFirst bool) (no int) {
	no = deck.DeckTroopNo()
	if isFirst {
		no = no + 1
	}
	no = (no / 2) + deck.oppTroops
	return no
}

//BotDrawNo calculate the bots number of the unknown cards.
func (deck *Deck) BotDrawNo(isFirst bool) (no int) {
	no = deck.DeckTroopNo()
	if isFirst {
		no = no + 1
	}
	no = no / 2
	return no
}

//initDecks initialize the deck content to all cards.
func initDecks(troops map[int]bool, tacs map[int]bool) {
	for i := 1; i <= cards.NOTroop; i++ {
		troops[i] = true
	}
	for i := cards.NOTroop + 1; i <= cards.NOTac+cards.NOTroop; i++ {
		tacs[i] = true
	}
	return
}

//Reset reset the deck to its initial state.
func (d *Deck) Reset() {
	d.oppTroops = bat.NOHandInit
	d.oppTacs = 0
	initDecks(d.troops, d.tacs)
	d.scoutReturnTacs = d.scoutReturnTacs[:0]
	d.scoutReturnTroops = d.scoutReturnTroops[:0]
	d.oppHand = d.oppHand[:0]
	d.tfSRMTacs = d.tfSRMTacs[:0]
	d.tfSRMTroops = d.tfSRMTroops[:0]
	d.oppKnowsCardixs = d.oppKnowsCardixs[:0]
	d.oppKnowTacsNo = 0
	d.oppKnowTroopsNo = 0
}

//InitRemoveCard removes a cards from decks.
//Warning does not include scout return it should be used
//for init. moves.
func (d *Deck) InitRemoveCards(cardixs []int) {
	for _, cardix := range cardixs {
		if cards.IsTroop(cardix) {
			delete(d.troops, cardix)
		} else {
			delete(d.tacs, cardix)
		}
	}
}

//PlayDraw updates the deck with a card drawn by the bot.
func (d *Deck) PlayDraw(cardix int) {
	if cards.IsTroop(cardix) {
		nscout := len(d.scoutReturnTroops)
		if nscout == 0 {
			delete(d.troops, cardix)
		} else {
			d.scoutReturnTroops = d.scoutReturnTroops[:nscout-1]
		}
		if d.oppKnowTroopsNo > 0 {
			d.oppKnowsCardixs = append(d.oppKnowsCardixs, cardix)
			d.oppKnowTroopsNo = d.oppKnowTroopsNo - 1
		}
	} else {
		nscout := len(d.scoutReturnTacs)
		if nscout == 0 {
			delete(d.tacs, cardix)
		} else {
			d.scoutReturnTacs = d.scoutReturnTacs[:nscout-1]
		}
		if d.oppKnowTacsNo > 0 {
			d.oppKnowsCardixs = append(d.oppKnowsCardixs, cardix)
			d.oppKnowTacsNo = d.oppKnowTacsNo - 1
		}
	}
}

//OppPlay update the deck with a card played by the opponent of the bot.
func (d *Deck) OppPlay(cardix int) {
	nHand := len(d.oppHand)
	if nHand != 0 {
		d.oppHand = slice.Remove(d.oppHand, cardix)
	}
	if nHand == len(d.oppHand) {
		if cards.IsTroop(cardix) {
			delete(d.troops, cardix)
			d.oppTroops = d.oppTroops - 1
		} else {
			delete(d.tacs, cardix)
			d.oppTacs = d.oppTacs - 1
		}
	}
}

//OppDraw update the deck with a card drawn by the opponent of the bot.
func (d *Deck) OppDraw(troop bool) {
	if troop {
		nscout := len(d.scoutReturnTroops)
		if nscout == 0 {
			d.oppTroops = d.oppTroops + 1
		} else {
			d.oppHand = append(d.oppHand, d.scoutReturnTroops[nscout-1])
			d.scoutReturnTroops = d.scoutReturnTroops[:nscout-1]
		}
		if d.oppKnowTroopsNo > 0 {
			d.oppKnowTroopsNo = d.oppKnowTroopsNo - 1
		}
	} else {
		nscout := len(d.scoutReturnTacs)
		if nscout == 0 {
			d.oppTacs = d.oppTacs + 1
		} else {
			d.oppHand = append(d.oppHand, d.scoutReturnTacs[nscout-1])
			d.scoutReturnTacs = d.scoutReturnTacs[:nscout-1]
		}
		if d.oppKnowTacsNo > 0 {
			d.oppKnowTroopsNo = d.oppKnowTacsNo - 1
		}
	}
}

//OppSetInitHand sets the opponents initial hand.
//Only used when restarting a old game.
//The deck initialize with 7 troops card.
func (d *Deck) OppSetInitHand(troops int, tacs int) {
	d.oppTacs = tacs
	d.oppTroops = troops
}

//OppScoutReturn update the deck with the opponent scout return move.
func (d *Deck) OppScoutReturn(troops int, tacs int) {
	d.oppTroops = d.oppTroops - troops
	d.oppTacs = d.oppTacs - tacs
	d.oppKnowTacsNo = tacs
	d.oppKnowTroopsNo = troops
}

//PlayScoutReturn registor a scout return move. Cards are delt from the back.
func (d *Deck) PlayScoutReturn(troops []int, tacs []int) {
	d.scoutReturnTroops = troops
	d.scoutReturnTacs = tacs
	d.tfSRMTacs = make([]int, len(tacs))
	d.tfSRMTroops = make([]int, len(troops))
	copy(d.tfSRMTacs, tacs)
	copy(d.tfSRMTroops, troops)
}

//DeckTacNo returns current the tactic card deck size.
func (d *Deck) DeckTacNo() int {
	return len(d.tacs) - d.oppTacs + len(d.scoutReturnTacs)
}

//DeckTroopNo returns the current troop deck size.
func (d *Deck) DeckTroopNo() int {
	return len(d.troops) - d.oppTroops + len(d.scoutReturnTroops)
}

//MaxValues returns the 4 max value a deck contain.
func (d *Deck) MaxValues() (values []int) {
	return maxValues(d.troops, d.scoutReturnTroops)
}
func maxValues(troops map[int]bool, scoutReturnTroops []int) (values []int) {
	values = make([]int, 0, 4)
	max := false
	for troopix := range troops {
		values, max = MaxValuesUpd(troopix, values)
		if max {
			break
		}
	}
	if !max {
		for _, troopix := range scoutReturnTroops {
			values, max = MaxValuesUpd(troopix, values)
			if max {
				break
			}
		}
	}

	return values
}

// MaxValuesUpd update the max value list with a card.
// #values
func MaxValuesUpd(troopix int, values []int) (updValues []int, max bool) {
	troop, _ := cards.DrTroop(troopix)
	troopValue := troop.Value()
	updValues = values
	cardNo := 4
	upd := false
	for ix, value := range updValues {
		if troopValue > value {
			if len(updValues) < cardNo {
				updValues = append(updValues, 0)
				copy(updValues[ix+1:], updValues[ix:])
			} else {
				copy(updValues[ix+1:], updValues[ix:cardNo-1])
			}
			updValues[ix] = troopValue
			upd = true
			break
		}
	}
	if !upd && len(updValues) < cardNo {
		updValues = append(updValues, troopValue)
	}

	if len(updValues) == cardNo && updValues[cardNo-1] == 10 {
		max = true
	}
	return updValues, max
}
func (deck *Deck) OppTacNo() int {
	return deck.oppTacs
}
func (deck *Deck) OppTroopNo() int {
	return deck.oppTroops
}
func (deck *Deck) ScoutReturnTacPeek() int {
	if len(deck.scoutReturnTacs) > 0 {
		return deck.scoutReturnTacs[len(deck.scoutReturnTacs)-1]
	}
	return 0
}
func (deck *Deck) ScoutReturnTroopPeek() int {
	if len(deck.scoutReturnTroops) > 0 {
		return deck.scoutReturnTroops[len(deck.scoutReturnTroops)-1]
	}
	return 0
}
func (deck *Deck) TfScoutReturnMoveTacs() []int {
	return deck.tfSRMTacs
}
func (deck *Deck) TfScoutReturnMoveTroops() []int {
	return deck.tfSRMTroops
}
func (deck *Deck) OppKnowns() (noTacs, noTroops int, handCardixs []int) {
	return deck.oppKnowTacsNo, deck.oppKnowTroopsNo, deck.oppKnowsCardixs
}
