package profilecontacts

import (
	"context"
	"errors"
	"sync"
	"testing"

	profilev1 "buf.build/gen/go/antinvestor/profile/protocolbuffers/go/profile/v1"
	"connectrpc.com/connect"
)

type fakeProfile struct {
	mu       sync.Mutex
	byDetail map[string]*profilev1.ContactObject
	byID     map[string]*profilev1.ContactObject
	creates  int
	gets     int
	fail     error
	seq      int
}

func (f *fakeProfile) CreateContact(
	_ context.Context,
	req *connect.Request[profilev1.CreateContactRequest],
) (*connect.Response[profilev1.CreateContactResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	if f.fail != nil {
		return nil, f.fail
	}
	detail := req.Msg.GetContact()
	key := detailKey(detail)
	if c, ok := f.byDetail[key]; ok {
		return connect.NewResponse(&profilev1.CreateContactResponse{Data: c}), nil
	}
	f.seq++
	c := &profilev1.ContactObject{
		Id:     "ct_" + itoa(f.seq),
		Detail: detail,
		Type:   profilev1.ContactType_EMAIL,
	}
	if !containsAt(detail) {
		c.Type = profilev1.ContactType_MSISDN
	}
	f.byDetail[key] = c
	f.byID[c.Id] = c
	return connect.NewResponse(&profilev1.CreateContactResponse{Data: c}), nil
}

func (f *fakeProfile) GetContacts(
	_ context.Context,
	req *connect.Request[profilev1.GetContactsRequest],
) (*connect.Response[profilev1.GetContactsResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.fail != nil {
		return nil, f.fail
	}
	var data []*profilev1.ContactObject
	var missing []string
	for _, id := range req.Msg.GetIds() {
		if c, ok := f.byID[id]; ok {
			data = append(data, c)
		} else {
			missing = append(missing, id)
		}
	}
	return connect.NewResponse(&profilev1.GetContactsResponse{
		Data:       data,
		MissingIds: missing,
	}), nil
}

func containsAt(s string) bool {
	for _, r := range s {
		if r == '@' {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestEnsureAndResolve(t *testing.T) {
	f := &fakeProfile{
		byDetail: map[string]*profilev1.ContactObject{},
		byID:     map[string]*profilev1.ContactObject{},
	}
	dir := &Service{Client: f}

	refs, err := dir.EnsureDetails(context.Background(),
		[]string{"work@acme.com", "+256711111111"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	ids := IDs(refs)
	if len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}

	// Resolve one
	one, miss, err := dir.Resolve(context.Background(), ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || len(miss) != 0 {
		t.Fatalf("one=%v miss=%v", one, miss)
	}
	if one[0].Detail == "" {
		t.Fatal("expected detail from GetContacts")
	}

	// Resolve many + missing
	many, miss, err := dir.Resolve(context.Background(), append(ids, "ct_missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(many) != 2 || len(miss) != 1 {
		t.Fatalf("many=%v miss=%v", many, miss)
	}
}

func TestEnsureDetails_KeepsKnownIDs(t *testing.T) {
	f := &fakeProfile{
		byDetail: map[string]*profilev1.ContactObject{},
		byID:     map[string]*profilev1.ContactObject{},
	}
	dir := &Service{Client: f}
	refs, err := dir.EnsureDetails(context.Background(),
		[]string{"a@b.com"},
		[]string{"ct_prev"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if IDs(refs)[0] != "ct_prev" {
		t.Fatalf("want known id first, got %v", IDs(refs))
	}
}

func TestEnsureDetails_Failure(t *testing.T) {
	f := &fakeProfile{
		byDetail: map[string]*profilev1.ContactObject{},
		byID:     map[string]*profilev1.ContactObject{},
		fail:     errors.New("denied"),
	}
	dir := &Service{Client: f}
	_, err := dir.EnsureDetails(context.Background(), []string{"a@b.com"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNilDirectory(t *testing.T) {
	var d Directory = Nil{}
	if _, err := d.EnsureDetails(context.Background(), []string{"a@b.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Resolve(context.Background(), []string{"ct_1"}); err != nil {
		t.Fatal(err)
	}
}
