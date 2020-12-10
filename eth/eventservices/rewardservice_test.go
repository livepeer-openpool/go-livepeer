package eventservices

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/golang/glog"
	"github.com/livepeer/go-livepeer/eth"
	lpTypes "github.com/livepeer/go-livepeer/eth/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	assert := assert.New(t)
	rs := RewardService{
		working: true,
	}
	assert.EqualError(rs.Start(context.Background()), ErrRewardServiceStarted.Error())

	ctx, cancel := context.WithCancel(context.Background())
	rs = RewardService{
		tw:           &stubTimeWatcher{},
		cancelWorker: cancel,
	}
	errC := make(chan error)
	go func() { errC <- rs.Start(ctx) }()
	time.Sleep(1 * time.Second)
	assert.True(rs.working)
	cancel()
	err := <-errC
	assert.Nil(err)
}

func TestStop(t *testing.T) {
	assert := assert.New(t)
	rs := RewardService{
		working: false,
	}
	assert.EqualError(rs.Stop(), ErrRewardServiceStopped.Error())

	ctx, cancel := context.WithCancel(context.Background())
	rs = RewardService{
		tw:           &stubTimeWatcher{},
		cancelWorker: cancel,
	}
	go rs.Start(ctx)
	time.Sleep(1 * time.Second)
	require.True(t, rs.working)
	rs.Stop()
	assert.False(rs.working)
}

func TestIsWorking(t *testing.T) {
	assert := assert.New(t)
	rs := RewardService{
		working: false,
	}
	assert.False(rs.IsWorking())
	rs.working = true
	assert.True(rs.IsWorking())
}

func TestReceiveRoundEvent_TryReward(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	eth := &eth.MockClient{}
	tw := &stubTimeWatcher{
		lastInitializedRound: big.NewInt(100),
	}
	ctx := context.Background()
	rs := RewardService{
		client: eth,
		tw:     tw,
	}

	go rs.Start(ctx)
	defer rs.Stop()
	time.Sleep(1 * time.Second)
	require.True(rs.IsWorking())

	// Happy case , check that reward was called
	// Assert that no error was logged
	eth.On("Account").Return(accounts.Account{})
	eth.On("GetTranscoder").Return(&lpTypes.Transcoder{
		LastRewardRound: big.NewInt(1),
		Active:          true,
	}, nil)
	eth.On("Reward").Return(&types.Transaction{}, nil).Times(1)
	eth.On("CheckTx").Return(nil).Times(1)
	eth.On("GetTranscoderEarningsPoolForRound").Return(&lpTypes.TokenPools{}, nil)

	errorLogsBefore := glog.Stats.Error.Lines()
	infoLogsBefore := glog.Stats.Info.Lines()

	tw.roundSink <- types.Log{}
	time.Sleep(1 * time.Second)

	eth.AssertNumberOfCalls(t, "Reward", 1)
	eth.AssertNumberOfCalls(t, "CheckTx", 1)
	eth.AssertNotCalled(t, "ReplaceTransaction")

	errorLogsAfter := glog.Stats.Error.Lines()
	infoLogsAfter := glog.Stats.Info.Lines()
	assert.Equal(int64(0), errorLogsAfter-errorLogsBefore)
	assert.Equal(int64(1), infoLogsAfter-infoLogsBefore)

	// Test for transaction time out error
	// Call replace transaction
	eth.On("Reward").Return(&types.Transaction{}, nil).Once()
	eth.On("CheckTx").Return(context.DeadlineExceeded).Once()
	eth.On("ReplaceTransaction").Return(&types.Transaction{}, nil)
	eth.On("CheckTx").Return(nil).Once()

	errorLogsBefore = glog.Stats.Error.Lines()
	infoLogsBefore = glog.Stats.Info.Lines()

	tw.roundSink <- types.Log{}
	time.Sleep(1 * time.Second)

	eth.AssertNumberOfCalls(t, "Reward", 2)
	eth.AssertNumberOfCalls(t, "CheckTx", 3)
	eth.AssertNumberOfCalls(t, "ReplaceTransaction", 1)

	errorLogsAfter = glog.Stats.Error.Lines()
	infoLogsAfter = glog.Stats.Info.Lines()
	assert.Equal(int64(0), errorLogsAfter-errorLogsBefore)
	assert.Equal(int64(2), infoLogsAfter-infoLogsBefore)

	// Test replacement timeout error
	eth.On("Reward").Return(&types.Transaction{}, nil).Once()
	eth.On("CheckTx").Return(context.DeadlineExceeded)
	eth.On("ReplaceTransaction").Return(&types.Transaction{}, nil)

	errorLogsBefore = glog.Stats.Error.Lines()
	infoLogsBefore = glog.Stats.Info.Lines()

	tw.roundSink <- types.Log{}
	time.Sleep(1 * time.Second)

	eth.AssertNumberOfCalls(t, "Reward", 3)
	eth.AssertNumberOfCalls(t, "CheckTx", 5)
	eth.AssertNumberOfCalls(t, "ReplaceTransaction", 2)

	errorLogsAfter = glog.Stats.Error.Lines()
	infoLogsAfter = glog.Stats.Info.Lines()
	assert.Equal(int64(1), errorLogsAfter-errorLogsBefore)
	assert.Equal(int64(1), infoLogsAfter-infoLogsBefore)
}

type stubTimeWatcher struct {
	lastInitializedRound *big.Int
	roundSink            chan<- types.Log
	roundSub             event.Subscription
}

func (m *stubTimeWatcher) SubscribeRounds(sink chan<- types.Log) event.Subscription {
	m.roundSink = sink
	m.roundSub = &stubSubscription{errCh: make(<-chan error)}
	return m.roundSub
}

func (m *stubTimeWatcher) LastInitializedRound() *big.Int {
	return m.lastInitializedRound
}

type stubSubscription struct {
	errCh        <-chan error
	unsubscribed bool
}

func (s *stubSubscription) Unsubscribe() {
	s.unsubscribed = true
}

func (s *stubSubscription) Err() <-chan error {
	return s.errCh
}
