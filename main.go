package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
)

var address = "localhost:8080"

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var templates = template.Must(template.New("base").ParseFiles("static/templates/entry.tmpl", "static/templates/game.tmpl", "static/templates/mainjs.tmpl", "static/templates/style.tmpl"))

var BoardN = 30

var turnDuration time.Duration = 30 * time.Second

var logger *log.Logger

type AiPoint struct {
	Reit int
	Point
}

type Board [][]int

func NewBoard(n int) Board {
	b := make([][]int, n, n)
	for i := range b {
		b[i] = make([]int, n, n)
	}
	return b
}

type Point struct {
	Abs int `json:"abs"`
	Ord int `json:"ord"`
}

type Man struct {
	Nic  string `json:"nic"`
	Mark int    `json:"mark"`
}

type Finish struct {
	Man
	Begin Point `json:"begin"`
	End   Point `json:"end"`
}

type Turn struct {
	Man
	Point `json:"point"`
}

type Registered struct {
	Man
	Passw string
}

type Common struct {
	Leaders []Man
	Active  map[string]int
	L       *sync.RWMutex
}

func (c *Common) EditLeaders(t Turn) {
	c.L.Lock()
	p := false
	for i, m := range c.Leaders {
		if m.Nic == t.Nic {
			p = true
			m.Mark++
			break
		}
		j := i
		for ; (j >= 0) && (c.Leaders[j].Mark < m.Mark); i-- {
		}
		c.Leaders[j], c.Leaders[i] = c.Leaders[i], c.Leaders[j]
	}
	if !p {
		c.Leaders = append(c.Leaders, Man{t.Nic, 1})
	}
	c.L.Unlock()
}

func (c *Common) AddActive(p *Player) {
	c.L.Lock()
	if _, ok := c.Active[p.Nic]; !ok {
		c.Active[p.Nic] = p.Mark
	}
	c.L.Unlock()
}
func (c *Common) RemoveActive(p *Player) {
	c.L.Lock()
	delete(c.Active, p.Nic)
	c.L.Unlock()
}

var common = &Common{
	Leaders: make([]Man, 0),
	Active:  make(map[string]int),
	L:       new(sync.RWMutex),
}

type Player struct {
	Man
	*websocket.Conn
	In chan Turn
	*Game
}

func (p *Player) ServeConn() {
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic Player ServeConn: %v", x)
		}
	}()
	defer p.Conn.Close()
	var t Turn
	for {
		if p.IfConnRead(&t) {
			switch {
			case (t.Nic == "go") && (t.Mark == 777): // API  read Start from connection
				if p.Game == nil {
					disputcher.In <- p
				}
			default:
				if p.Game != nil {
					p.Game.In <- t
				} else {
					// todo
				}
			}
		} else {
			return
		}
	}
}

type Ctl struct {
	Control string `json:"control"`
}

type EndMsg struct {
	Ctl
	Finish
}

func (p *Player) IfConnRead(m interface{}) bool {
	if err := p.Conn.ReadJSON(m); err != nil { // API write Finish to connection
		//todo err
		if p.Game != nil {
			p.Game.CleanUp1(p.In)
			p.Game.UpdateState()
		}
		close(p.In)
		common.RemoveActive(p)
		return false
	} else {
		return true
	}
}

func (p *Player) ListenGame() {
	var r Reg
	r.Players = make([]string, 0)
	r.Control = "reg"
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic player ListenGame: %v", x)
		}
	}()
	for t := range p.In {

		switch t.Nic {
		case "control start":
			if err := p.Conn.WriteJSON(Ctl{"start"}); err != nil {
				logger.Println(err)
			}
		case "control done":
			if err := p.Conn.WriteJSON(EndMsg{
				Ctl:    Ctl{"done"},
				Finish: p.Game.Finish,
			}); err != nil {
				logger.Println(err)
			} else {
				p.Mark = p.Game.Order
				p.Game = nil
			}
		case "control reg":
			p.Mark = t.Mark
			p.Game.Shared.RLock()
			r.Players = p.Game.Shared.Nics
			r.Curr = p.Game.Shared.Curr
			p.Game.Shared.RUnlock()
			r.MyMark = p.Mark
			if err := p.Conn.WriteJSON(r); err != nil {
				logger.Println(err)
			}
		default:
			if err := p.Conn.WriteJSON(t); err != nil {
				logger.Println(err)
			}
		}
	}
}

type shared struct {
	NPlayers int // number of players
	Players  []chan Turn
	Nics     []string
	Curr     int
	*sync.RWMutex
}

type Game struct {
	Shared  shared
	Running bool
	Order   int
	Board
	//	Reit Board
	Finish
	In chan Turn
	Tm *time.Timer
	AiPoint
}

func InitGame(n int) *Game {
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic InitGame: %v", x)
		}
	}()
	g := new(Game)
	g.Reit = 0
	g.Abs = 15
	g.Ord = 15
	var sh shared
	// sh.NPlayers = n
	sh.Players = make([]chan Turn, 0, n)
	sh.Nics = make([]string, 0, n)
	sh.RWMutex = new(sync.RWMutex)
	g.Shared = sh
	g.Order = n
	g.Board = NewBoard(BoardN)
	//	g.Reit = NewBoard(BoardN)
	g.In = make(chan Turn, 0)
	return g
}

func (g *Game) Ann() {
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic Game Ann: %v", x)
		}
	}()
	var t Turn
	g.Tm = time.AfterFunc(turnDuration, g.MissTurn)
	t.Nic = "control start"
	g.Shared.RLock()
	for _, p := range g.Shared.Players {
		if p != nil {
			p <- t
		}
	}
	g.Shared.RUnlock()
}
func (g *Game) UpdateState() {
	g.Shared.RLock()
	for i, p := range g.Shared.Players {
		if p != nil {
			p <- Turn{Man: Man{
				Nic:  "control reg",
				Mark: i,
			},
			}
		}
	}
	g.Shared.RUnlock()
}

func (g *Game) Run() {
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic Game Run: %v", x)
		}
	}()
	for t := range g.In {
		g.Shared.RLock()
		if (g.Shared.Nics[g.Shared.Curr] == t.Nic) && (g.Shared.Curr == t.Mark) {
			g.Tm.Stop()
			if g.Check(&t) {
				for _, p := range g.Shared.Players {
					if p != nil {
						p <- t
					}
				}
				g.Shared.RUnlock()
				g.Finish.Man = t.Man
				common.EditLeaders(t)
				t.Nic = "control done"
				g.Shared.RLock()
				for _, p := range g.Shared.Players {
					if p != nil {
						p <- t
					}
				}
				g.Shared.Players = nil
				g.Shared.RUnlock()
				g.Tm = nil
				return
			} else {
				for _, p := range g.Shared.Players {
					if p != nil {
						p <- t
					}
				}
				g.Shared.RUnlock()
				g.Tm.Reset(turnDuration)
				g.Shared.RLock()
				g.Shared.Curr++
				if g.Shared.Curr >= g.Shared.NPlayers {
					g.Shared.Curr = 0
				}
				g.Shared.RUnlock()
			}
		} else {
			g.Shared.RUnlock()
		}
	}
}
func (g *Game) MissTurn() {
	var t Turn
	g.Shared.RLock()
	t.Nic = g.Shared.Nics[g.Shared.Curr]
	t.Mark = g.Shared.Curr
	g.Shared.RUnlock()
	if g.AiPoint.Reit == 1 {
		g.GetAiPoint()
	}
	t.Point = g.AiPoint.Point
	g.AiPoint.Reit = 1
	g.In <- t
}

func (g *Game) GetAiPoint() {
	for k, v := range g.Board {
		for n, m := range v {
			if m < g.AiPoint.Reit {
				g.AiPoint.Reit = m
				g.AiPoint.Abs = k
				g.AiPoint.Ord = n
			}
		}
	}
}

func (g *Game) Check(t *Turn) bool {
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic Game Check: %v", x)
		}
	}()
	var tm int
	x := t.Abs
	y := t.Ord
	if x < 0 || y < 0 || x > len(g.Board) || y > len(g.Board) {
		return false // todo
	}
	g.Shared.RLock()
	for k, v := range g.Shared.Nics {
		if v == t.Nic {
			tm = k + 1
		}
	}
	g.Shared.RUnlock()
	m := g.Board[x][y]
	if m > 0 {
		if g.AiPoint.Reit == 1 {
			g.GetAiPoint()
		}
		t.Point = g.AiPoint.Point
		g.AiPoint.Reit = 1
	}
	x = t.Abs
	y = t.Ord
	b := Point{x, y}
	e := Point{x, y}
	g.Board[x][y] = 0
	g.Board[x][y] = tm
	var i, j, i1, j1 int
	score := 0
	for i = x - 1; i >= 0 && g.Board[i][y] == tm; i-- {
		score++
		b = Point{i, y}
	}
	for i1 = x + 1; i1 < len(g.Board) && g.Board[i1][y] == tm; i1++ {
		score++
		e = Point{i1, y}
	}
	if i > 0 && g.Board[i][y] <= 0 {
		g.Board[i][y] = g.Board[i][y] - score - 1
		if g.Board[i][y] < g.AiPoint.Reit {
			g.AiPoint.Abs = i
			g.AiPoint.Ord = y
			g.AiPoint.Reit = g.Board[i][y]
		}
	}
	if i1 < len(g.Board)-1 && g.Board[i1][y] <= 0 {
		g.Board[i1][y] = g.Board[i1][y] - score - 1
		if g.Board[i1][y] < g.AiPoint.Reit {
			g.AiPoint.Abs = i1
			g.AiPoint.Ord = y
			g.AiPoint.Reit = g.Board[i1][y]
		}

	}
	if score > 3 {
		g.Finish.Begin, g.Finish.End = b, e
		return true
	}
	score = 0
	for i = y - 1; i >= 0 && g.Board[x][i] == tm; i-- {
		score++
		b = Point{x, i}
	}
	for i1 = y + 1; i1 < len(g.Board) && g.Board[x][i1] == tm; i1++ {
		score++
		e = Point{x, i1}
	}
	if i > 0 && g.Board[x][i] <= 0 {
		g.Board[x][i] = g.Board[x][i] - score - 1
		if g.Board[x][i] < g.AiPoint.Reit {
			g.AiPoint.Abs = x
			g.AiPoint.Ord = i
			g.AiPoint.Reit = g.Board[x][i]
		}

	}
	if i1 < len(g.Board)-1 && g.Board[x][i1] <= 0 {
		g.Board[x][i1] = g.Board[x][i1] - score - 1
		if g.Board[x][i1] < g.AiPoint.Reit {
			g.AiPoint.Abs = x
			g.AiPoint.Ord = i1
			g.AiPoint.Reit = g.Board[x][i1]
		}

	}
	if score > 3 {
		g.Finish.Begin, g.Finish.End = b, e
		return true
	}
	score = 0
	for i, j = x-1, y-1; i >= 0 && j >= 0 && g.Board[i][j] == tm; {
		b = Point{i, j}
		i--
		j--
		score++
	}
	for i1, j1 = x+1, y+1; i1 < len(g.Board) && j1 < len(g.Board) && g.Board[i1][j1] == tm; {
		e = Point{i1, j1}
		i1++
		j1++
		score++
	}
	if i > 0 && j > 0 && g.Board[i][j] <= 0 {
		g.Board[i][j] = g.Board[i][j] - score - 1
		if g.Board[i][j] < g.AiPoint.Reit {
			g.AiPoint.Abs = i
			g.AiPoint.Ord = j
			g.AiPoint.Reit = g.Board[i][j]
		}

	}

	if i1 < len(g.Board)-1 && j1 < len(g.Board)-1 && g.Board[i1][j1] <= 0 {
		g.Board[i1][j1] = g.Board[i1][j1] - score - 1
		if g.Board[i1][j1] < g.AiPoint.Reit {
			g.AiPoint.Abs = i1
			g.AiPoint.Ord = j1
			g.AiPoint.Reit = g.Board[i1][j1]
		}

	}
	if score > 3 {
		g.Finish.Begin, g.Finish.End = b, e
		return true
	}
	score = 0
	for i, j = x-1, y+1; i >= 0 && j < len(g.Board) && g.Board[i][j] == tm; {
		b = Point{i, j}
		i--
		j++
		score++
	}
	for i1, j1 = x+1, y-1; i1 < len(g.Board) && j1 >= 0 && g.Board[i1][j1] == tm; {
		e = Point{i1, j1}
		i1++
		j1--
		score++
	}
	if i > 0 && j < len(g.Board)-1 && g.Board[i][j] <= 0 {
		g.Board[i][j] = g.Board[i][j] - score - 1
		if g.Board[i][j] < g.AiPoint.Reit {
			g.AiPoint.Abs = i
			g.AiPoint.Ord = j
			g.AiPoint.Reit = g.Board[i][j]
		}

	}
	if i1 < len(g.Board)-1 && j > 0 && g.Board[i1][j1] <= 0 {
		g.Board[i1][j1] = g.Board[i1][j1] - score - 1
		if g.Board[i1][j1] < g.AiPoint.Reit {
			g.AiPoint.Abs = i1
			g.AiPoint.Ord = j1
			g.AiPoint.Reit = g.Board[i1][j1]
		}
	}
	if score > 3 {
		g.Finish.Begin, g.Finish.End = b, e
		return true
	}
	return false
}

func (g *Game) CleanUp1(pc chan Turn) {
	var i int
	var ok bool
	var c chan Turn
	g.Shared.Lock()
	for i, c = range g.Shared.Players {
		if c == pc {
			ok = true
			break
		}
	}
	if !ok {
		g.Shared.Unlock()
		log.Println("false cleanup")
		return
	} else {
		g.Shared.Players[i] = g.Shared.Players[len(g.Shared.Players)-1]
		g.Shared.Players[len(g.Shared.Players)-1] = nil
		g.Shared.Players = g.Shared.Players[:len(g.Shared.Players)-1]
		g.Shared.Nics[i] = g.Shared.Nics[len(g.Shared.Nics)-1]
		g.Shared.Nics = g.Shared.Nics[:len(g.Shared.Nics)-1]
		if (g.Shared.Curr > 0) && ((g.Shared.Curr > i) || (g.Shared.Curr == i) && (g.Shared.NPlayers == i+1)) {
			g.Shared.Curr--
		}
		g.Shared.NPlayers--
		g.Shared.Unlock()
		if g.Shared.NPlayers == 0 && g.Running {
			g.Tm.Stop()
			g.Tm = nil
			close(g.In)
			g.Running = false
		}
	}
}

type Disputcher struct {
	Games [4]*Game
	In    chan *Player
}

var disputcher = &Disputcher{
	Games: [4]*Game{nil, nil, nil, nil},
	In:    make(chan *Player, 0),
}

type Reg struct {
	Ctl
	Players []string `json:"players"`
	Curr    int      `json:"curr"`
	MyMark  int      `json:"mymark"`
}

func (d *Disputcher) Disputch() {
	defer func() {
		go disputcher.Disputch()
		if x := recover(); x != nil {
			logger.Printf("run time panic disputcher: %v", x)
		}
	}()
	for p := range d.In {
		ord := p.Mark
		i := ord - 2
		if d.Games[i] == nil {
			d.Games[i] = InitGame(ord)
		}
		d.Games[i].Shared.Lock()
		d.Games[i].Shared.Players = append(d.Games[i].Shared.Players, p.In)
		d.Games[i].Shared.NPlayers++
		d.Games[i].Shared.Nics = append(d.Games[i].Shared.Nics, p.Nic)
		p.Mark = len(d.Games[i].Shared.Players)
		d.Games[i].Shared.Unlock()
		if p.Game == nil {
			p.Game = d.Games[i]
		}
		d.Games[i].UpdateState()
		d.Games[i].Shared.RLock()
		if len(d.Games[i].Shared.Players) == ord {
			d.Games[i].Shared.RUnlock()
			d.Games[i].Ann()
			d.Games[i].Running = true
			go d.Games[i].Run()
			d.Games[i] = nil
		} else {
			d.Games[i].Shared.RUnlock()
		}
	}
}

func Draw(w http.ResponseWriter, r *http.Request) {
	var err error
	err = r.ParseForm()
	if err != nil {
		// todo
	}
	var g = struct {
		Nic      string
		NPlayers string
		Address	string
	}{r.Form.Get("nic"), r.Form.Get("nplayers"), address}
	err = templates.ExecuteTemplate(w, "game", g)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
func Entry(w http.ResponseWriter, r *http.Request) {
	common.L.RLock()
	err := templates.ExecuteTemplate(w, "entry", common)
	common.L.RUnlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func HandlePlayer(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("run time panic HandlePlayer: %v", x)
		}

	}()
	var err error
	err = r.ParseForm()
	if err != nil {
		logger.Println(err)
	}
	p := new(Player)
	p.Nic = r.Form.Get("nic")
	p.Mark, err = strconv.Atoi(r.Form.Get("nplayers"))
	common.AddActive(p)
	if err != nil {
		logger.Println(err)
	}
	p.In = make(chan Turn, 0) // todo chan len
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Println(err)
	}
	p.Conn = conn
	go p.ServeConn()
	go p.ListenGame()
}
func main() {
	logfile, err := os.OpenFile("/home/uvelichitel/kostar.org/log", os.O_RDWR|os.O_CREATE, 0777)
	if err != nil {
		print(err)
	}
	logger = log.New(logfile, "logger:", log.Lshortfile)
	go disputcher.Disputch()
	http.HandleFunc("/", Entry)
	http.HandleFunc("/draw", Draw)
	http.HandleFunc("/ws", HandlePlayer)
	logger.Fatal(http.ListenAndServe(address, nil))
}