package harness

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestLongExecutionAcrossFormerTaskLimits(t *testing.T) {
	if os.Getenv("FREEAGENT_LONG_EXECUTION") != "1" { t.Skip("set FREEAGENT_LONG_EXECUTION=1 for the actual >300 second isolated execution") }
	calls:=0
	service,_,_:=testHarness(t,modelFunc(func(ctx context.Context,_ ModelRequest,_ func(string))(ModelResponse,error){
		calls++
		if calls>62 { return ModelResponse{Content:"completed beyond the former task limits"},nil }
		select{case <-ctx.Done(): return ModelResponse{},ctx.Err();case <-time.After(5*time.Second):}
		return ModelResponse{ToolCalls:[]ToolCall{makeCall(fmt.Sprintf("long-%d",calls),"file_list",map[string]string{"path":"."})}},nil
	}))
	start:=time.Now()
	result,err:=service.Run(t.Context(),conversation(t,service),"actual-long-execution","perform the bounded long execution fixture")
	if err!=nil || result.Run.Status!="completed" || result.Run.Steps<=100 || result.Run.ElapsedMS<=300000 || time.Since(start)<=300*time.Second {t.Fatalf("status=%s steps=%d elapsed_ms=%d wall=%v error=%v",result.Run.Status,result.Run.Steps,result.Run.ElapsedMS,time.Since(start),err)}
	t.Logf("actual wall=%v steps=%d active_ms=%d",time.Since(start),result.Run.Steps,result.Run.ElapsedMS)
}
