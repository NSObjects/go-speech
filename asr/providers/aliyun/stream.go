package aliyun

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "strings"
    "sync"
    "time"

    "github.com/NSObjects/go-speech/asr"
    "github.com/NSObjects/go-speech/speecherr"
    "github.com/gorilla/websocket"
)

var (
    ErrClosed = errors.New("aliyun: stream closed")
    ErrSendClosed = errors.New("aliyun: audio input finished")
)

// Stream is a task-specific WebSocket session. It permits one concurrent sender
// and one concurrent receiver. A canceled Send/Recv context closes the session.
type Stream struct {
    conn *websocket.Conn
    parent context.Context
    taskID string
    done chan struct{}
    once sync.Once
    writeMu sync.Mutex
    readMu sync.Mutex
    sendClosed bool // writeMu
    finished bool // readMu
}
var _ asr.Stream = (*Stream)(nil)

func newStream(conn *websocket.Conn,ctx context.Context,taskID string) *Stream {
    s:=&Stream{conn:conn,parent:ctx,taskID:taskID,done:make(chan struct{})}
    go func(){
        select{
        case <-ctx.Done():_ = s.Close()
        case <-s.done:
        }
    }()
    return s
}
func (s *Stream) Close() error {
    if s==nil{return nil}
    s.once.Do(func(){close(s.done);_ = s.conn.Close()})
    return nil
}
func (s *Stream) closed() bool {
    select{case <-s.done:return true;default:return false}
}
func (s *Stream) write(ctx context.Context,typ int,data []byte) error {
    if err:=ctx.Err();err!=nil{return err}
    if err:=s.parent.Err();err!=nil{return err}
    if s.closed(){return ErrClosed}
    stop:=context.AfterFunc(ctx,func(){_ = s.Close()})
    defer stop()
    deadline:=time.Now().Add(defaultWriteTimeout)
    if d,ok:=ctx.Deadline();ok&&d.Before(deadline){deadline=d}
    if err:=s.conn.SetWriteDeadline(deadline);err!=nil{return err}
    return s.conn.WriteMessage(typ,data)
}
func (s *Stream) writeJSON(ctx context.Context,v any) error {
    b,err:=json.Marshal(v)
    if err!=nil{return err}
    return s.write(ctx,websocket.TextMessage,b)
}
func (s *Stream) Send(ctx context.Context,chunk []byte) error {
    if s==nil{return ErrClosed}
    if len(chunk)==0{return badArgument("empty audio chunk")}
    s.writeMu.Lock()
    defer s.writeMu.Unlock()
    if s.sendClosed{return ErrSendClosed}
    if err:=s.write(ctx,websocket.BinaryMessage,chunk);err!=nil{return wrapNetwork(ctx,"send",err)}
    return nil
}
func (s *Stream) CloseSend(ctx context.Context) error {
    if s==nil{return ErrClosed}
    s.writeMu.Lock()
    defer s.writeMu.Unlock()
    if s.sendClosed{return nil}
    if s.closed(){return ErrClosed}
    cmd:=taskMessage{Header:commandHeader{Action:"finish-task",TaskID:s.taskID,Streaming:"duplex"},Payload:taskPayload{Input:map[string]any{}}}
    if err:=s.writeJSON(ctx,cmd);err!=nil{return wrapNetwork(ctx,"finish-task",err)}
    s.sendClosed=true
    return nil
}
func (s *Stream) Recv(ctx context.Context) (asr.Event,error) {
    if s==nil{return asr.Event{},ErrClosed}
    s.readMu.Lock()
    defer s.readMu.Unlock()
    if s.finished{return asr.Event{},io.EOF}
    if s.closed(){return asr.Event{},s.closeError(ctx)}
    stop:=context.AfterFunc(ctx,func(){_ = s.Close()})
    defer stop()
    for{
        if err:=ctx.Err();err!=nil{return asr.Event{},err}
        if err:=s.parent.Err();err!=nil{return asr.Event{},err}
        if d,ok:=ctx.Deadline();ok{
            if err:=s.conn.SetReadDeadline(d);err!=nil{_ = s.Close();return asr.Event{},wrapNetwork(ctx,"recv",err)}
        } else {
            if err:=s.conn.SetReadDeadline(time.Time{});err!=nil{_ = s.Close();return asr.Event{},wrapNetwork(ctx,"recv",err)}
        }
        _,data,err:=s.conn.ReadMessage()
        if err!=nil{_ = s.Close();return asr.Event{},wrapNetwork(ctx,"recv",err)}
        ev,err:=decodeEvent(data)
        if err!=nil{_ = s.Close();return asr.Event{},protocolError("recv",err)}
        if ev.Header.TaskID!=s.taskID{
            _ = s.Close()
            return asr.Event{},protocolError("recv",errors.New("task_id mismatch"))
        }
        switch ev.Header.Event{
        case "result-generated":
            sentence:=ev.Payload.Output.Sentence
            if sentence.Heartbeat{continue}
            if sentence.SentenceEnd{
                if sentence.Text==""{continue}
                return asr.Event{Type:asr.EventFinal,Text:sentence.Text},nil
            }
            if sentence.Text!=""{return asr.Event{Type:asr.EventPartial,Text:sentence.Text},nil}
            if sentence.SentenceBegin{return asr.Event{Type:asr.EventSpeechStart},nil}
        case "task-finished":
            s.finished=true
            _=s.Close()
            return asr.Event{},io.EOF
        case "task-failed":
            _=s.Close()
            return asr.Event{},taskFailure(ev.Header)
        case "task-started": // Duplicate notification, safe to ignore.
        default: // Forward-compatible optional notifications.
        }
    }
}
func (s *Stream) closeError(ctx context.Context) error {
    if err:=ctx.Err();err!=nil{return err}
    if err:=s.parent.Err();err!=nil{return err}
    return ErrClosed
}

type commandHeader struct {
    Action string `json:"action"`
    TaskID string `json:"task_id"`
    Streaming string `json:"streaming"`
}
type taskPayload struct {
    TaskGroup string `json:"task_group,omitempty"`
    Task string `json:"task,omitempty"`
    Function string `json:"function,omitempty"`
    Model string `json:"model,omitempty"`
    Parameters map[string]any `json:"parameters,omitempty"`
    Input map[string]any `json:"input"`
}
type taskMessage struct {
    Header commandHeader `json:"header"`
    Payload taskPayload `json:"payload"`
}
type responseHeader struct {
    Event string `json:"event"`
    TaskID string `json:"task_id"`
    ErrorCode string `json:"error_code"`
    ErrorMessage string `json:"error_message"`
}
type responseSentence struct {
    Text string `json:"text"`
    SentenceBegin bool `json:"sentence_begin"`
    SentenceEnd bool `json:"sentence_end"`
    Heartbeat bool `json:"heartbeat"`
}
type responsePayload struct {
    Output struct { Sentence responseSentence `json:"sentence"` } `json:"output"`
}
type responseEvent struct {
    Header responseHeader `json:"header"`
    Payload responsePayload `json:"payload"`
}
func decodeEvent(data []byte) (responseEvent,error){
    var ev responseEvent
    if err:=json.Unmarshal(data,&ev);err!=nil{return ev,err}
    if ev.Header.Event==""||ev.Header.TaskID==""{return ev,errors.New("missing event or task_id")}
    return ev,nil
}
func protocolError(op string,err error) error{
    return &speecherr.Error{Code:speecherr.CodeInternal,Provider:"aliyun",Operation:op,Err:err}
}
func taskFailure(h responseHeader) error{
    code:=speecherr.CodeInternal
    desc:=strings.ToLower(h.ErrorCode+" "+h.ErrorMessage)
    switch{
    case strings.Contains(desc,"auth")||strings.Contains(desc,"permission"):code=speecherr.CodeUnauthorized
    case strings.Contains(desc,"quota")||strings.Contains(desc,"limit")||strings.Contains(desc,"throttl"):code=speecherr.CodeRateLimited
    case strings.Contains(desc,"timeout"):code=speecherr.CodeTimeout
    case strings.Contains(desc,"unavailable"):code=speecherr.CodeUnavailable
    }
    return &speecherr.Error{Code:code,Provider:"aliyun",Operation:"task",Err:fmt.Errorf("remote error %q: %s",h.ErrorCode,h.ErrorMessage),Retryable:code==speecherr.CodeUnavailable||code==speecherr.CodeTimeout||code==speecherr.CodeRateLimited}
}
func (s *Stream) awaitStarted(ctx context.Context) error{
    _,data,err:=s.conn.ReadMessage()
    if err!=nil{return wrapNetwork(ctx,"task-started",err)}
    ev,err:=decodeEvent(data)
    if err!=nil{return protocolError("task-started",err)}
    if ev.Header.TaskID!=s.taskID{return protocolError("task-started",errors.New("task_id mismatch"))}
    switch ev.Header.Event{
    case "task-started":return nil
    case "task-failed":return taskFailure(ev.Header)
    default:return protocolError("task-started",fmt.Errorf("unexpected event %q",ev.Header.Event))
    }
}
