// Copyright IBM Corp. All Rights Reserved.
//
// SPDX-License-Identifier: Apache-2.0
//

package naive

import (
	"crypto/sha256"
	"encoding/hex"
	"sync/atomic"
	"time"

	smart "github.com/SmartBFT-Go/consensus/pkg/api"
	smartbft "github.com/SmartBFT-Go/consensus/pkg/consensus"
	bft "github.com/SmartBFT-Go/consensus/pkg/types"
	"github.com/SmartBFT-Go/consensus/pkg/wal"
	protos "github.com/SmartBFT-Go/consensus/smartbftprotos"
	"github.com/golang/protobuf/proto"
)

type Ingress map[int]<-chan *protos.Message
type Egress map[int]chan<- *protos.Message

type Node struct {
	stopChan    chan struct{}
	nextSeq     uint64
	prevHash    string
	id          uint64
	in          Ingress
	out         Egress
	deliverChan chan<- *Block
	consensus   *smartbft.Consensus
}

func (*Node) Sync() (protos.ViewMetadata, uint64) {
	panic("implement me")
}

func (*Node) RequestID(req []byte) bft.RequestInfo {
	txn := TransactionFromBytes(req)
	return bft.RequestInfo{
		ClientID: txn.ClientID,
		ID:       txn.Id,
	}
}

func (*Node) VerifyProposal(proposal bft.Proposal) ([]bft.RequestInfo, error) {
	blockData := BlockDataFromBytes(proposal.Payload)
	requests := make([]bft.RequestInfo, 0)
	for _, t := range blockData.Transactions {
		tx := TransactionFromBytes(t)
		reqInfo := bft.RequestInfo{ID: tx.Id, ClientID: tx.ClientID}
		requests = append(requests, reqInfo)
	}
	return requests, nil
}

func (*Node) VerifyRequest(val []byte) (bft.RequestInfo, error) {
	return bft.RequestInfo{}, nil
}

func (*Node) VerifyConsenterSig(_ bft.Signature, prop bft.Proposal) error {
	return nil
}

func (*Node) VerificationSequence() uint64 {
	return 0
}

func (*Node) Sign(msg []byte) []byte {
	return nil
}

func (n *Node) SignProposal(bft.Proposal) *bft.Signature {
	return &bft.Signature{
		Id: n.id,
	}
}

func (n *Node) AssembleProposal(metadata []byte, requests [][]byte) (nextProp bft.Proposal, remainder [][]byte) {
	blockData := BlockData{Transactions: requests}.ToBytes()
	return bft.Proposal{
		Header: BlockHeader{
			PrevHash: n.prevHash,
			DataHash: computeDigest(blockData),
			Sequence: int64(atomic.LoadUint64(&n.nextSeq)),
		}.ToBytes(),
		Payload: BlockData{Transactions: requests}.ToBytes(),
		Metadata: marshalOrPanic(&protos.ViewMetadata{
			LatestSequence: n.nextSeq,
			ViewId:         0, // TODO: change this when implementing view change
		}),
	}, nil
}

func marshalOrPanic(msg proto.Message) []byte {
	b, err := proto.Marshal(msg)
	if err != nil {
		panic(err)
	}
	return b
}

func (n *Node) BroadcastConsensus(m *protos.Message) {
	for receiver, out := range n.out {
		if n.id == uint64(receiver) {
			continue
		}
		out <- m
	}
}

func (n *Node) SendConsensus(targetID uint64, message *protos.Message) {
	n.out[int(targetID)] <- message
}

func (n *Node) SendTransaction(targetID uint64, request []byte) {
	// TODO: Handle send transaction request
}

func (n *Node) Deliver(proposal bft.Proposal, signature []bft.Signature) {
	blockData := BlockDataFromBytes(proposal.Payload)
	var txns []Transaction
	for _, rawTxn := range blockData.Transactions {
		txn := TransactionFromBytes(rawTxn)
		txns = append(txns, Transaction{
			ClientID: txn.ClientID,
			Id:       txn.Id,
		})
	}
	header := BlockHeaderFromBytes(proposal.Header)
	atomic.AddUint64(&n.nextSeq, 1)
	n.deliverChan <- &Block{
		Sequence:     uint64(header.Sequence),
		PrevHash:     header.PrevHash,
		Transactions: txns,
	}
}

func NewNode(id uint64, in Ingress, out Egress, deliverChan chan<- *Block, logger smart.Logger) *Node {
	node := &Node{
		id:          id,
		in:          in,
		out:         out,
		deliverChan: deliverChan,
		stopChan:    make(chan struct{}),
	}
	node.consensus = &smartbft.Consensus{
		SelfID:           id,
		N:                4,
		BatchSize:        1,
		BatchTimeout:     10 * time.Millisecond,
		Logger:           logger,
		Comm:             node,
		Signer:           node,
		Verifier:         node,
		Application:      node,
		Assembler:        node,
		RequestInspector: node,
		Synchronizer:     node,
		WAL:              &wal.EphemeralWAL{},
	}
	node.consensus.Start()
	node.Start()
	return node
}

func (n *Node) Start() {
	for id, in := range n.in {
		if uint64(id) == n.id {
			continue
		}
		go func(id uint64, in <-chan *protos.Message) {
			for {
				select {
				case <-n.stopChan:
					return
				case msg := <-in:
					n.consensus.HandleMessage(id, msg)
				}
			}
		}(uint64(id), in)
	}
}

func computeDigest(rawBytes []byte) string {
	h := sha256.New()
	h.Write(rawBytes)
	digest := h.Sum(nil)
	return hex.EncodeToString(digest)
}
