package ipc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"myguo/internal/qrauth"
)

// QRLoginDTO contains presentation data only; Passport tokens and cookies stay
// inside the attempt's private jar until phone confirmation completes.
type QRLoginDTO struct {
	ID        string `json:"id"`
	Image     []byte `json:"image"`
	ExpiresAt int64  `json:"expiresAt"`
}
type QRLoginStatusDTO struct {
	State   string       `json:"state"`
	Profile *UserProfile `json:"profile,omitempty"`
}
type qrLoginAttempt struct {
	id                   string
	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	session              *qrauth.Session
	ownerID, ownerCookie string
}

func (s *HongguoService) BeginQRLogin(ctx context.Context) (*QRLoginDTO, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	ownerID, ownerCookie, _ := s.authSession()
	attempt := &qrLoginAttempt{id: uuid.NewString(), ctx: ctx, cancel: cancel, ownerID: ownerID, ownerCookie: ownerCookie}
	s.qrMu.Lock()
	if s.qrActive != nil {
		s.qrActive.cancel()
	}
	s.qrActive = attempt
	s.qrMu.Unlock()
	context.AfterFunc(ctx, func() {
		s.qrMu.Lock()
		if s.qrActive == attempt {
			s.qrActive = nil
		}
		s.qrMu.Unlock()
	})
	session, err := qrauth.New(ctx, s.httpClient.Transport)
	s.qrMu.Lock()
	defer s.qrMu.Unlock()
	if err != nil || s.qrActive != attempt || ctx.Err() != nil {
		cancel()
		if s.qrActive == attempt {
			s.qrActive = nil
		}
		if err != nil {
			return nil, err
		}
		return nil, context.Canceled
	}
	attempt.session = session
	return &QRLoginDTO{ID: attempt.id, Image: append([]byte(nil), session.Image...), ExpiresAt: session.ExpiresAt.Unix()}, nil
}

func (s *HongguoService) PollQRLogin(ctx context.Context, id string) (*QRLoginStatusDTO, error) {
	s.qrMu.Lock()
	attempt := s.qrActive
	s.qrMu.Unlock()
	if attempt == nil || attempt.id != id {
		return nil, errors.New("扫码会话已结束，请刷新二维码")
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if attempt.ctx.Err() != nil {
		return nil, attempt.ctx.Err()
	}
	pollCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(attempt.ctx, cancel)
	defer stop()
	defer cancel()
	state, credentials, err := attempt.session.Poll(pollCtx)
	if errors.Is(err, qrauth.ErrTemporary) {
		state = "waiting"
		if attempt.session.PhoneConfirmed() {
			state = "confirming"
		}
		return &QRLoginStatusDTO{State: state}, nil
	}
	if err != nil {
		return nil, err
	}
	if credentials == nil {
		return &QRLoginStatusDTO{State: state}, nil
	}
	// Serialize cancellation/new attempts/account changes with publication. A
	// failed or abandoned QR never replaces the currently authenticated account.
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	s.qrMu.Lock()
	defer s.qrMu.Unlock()
	ownerID, ownerCookie, _ := s.authSession()
	if s.qrActive != attempt || pollCtx.Err() != nil || attempt.ctx.Err() != nil {
		return nil, context.Canceled
	}
	if ownerID != attempt.ownerID || ownerCookie != attempt.ownerCookie {
		return nil, errors.New("账号状态已变化，请重新扫码")
	}
	profile, err := s.loginWithCookiesLocked(pollCtx, credentials.Cookies, credentials.Token)
	if errors.Is(err, qrauth.ErrTemporary) || errors.Is(err, context.DeadlineExceeded) {
		return &QRLoginStatusDTO{State: "confirming"}, nil
	}
	if err != nil {
		return nil, err
	}
	s.qrActive = nil
	attempt.cancel()
	return &QRLoginStatusDTO{State: "confirmed", Profile: profile}, nil
}
