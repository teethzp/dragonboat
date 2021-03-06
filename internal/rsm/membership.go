// Copyright 2017-2019 Lei Ni (nilei81@gmail.com) and other Dragonboat authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rsm

import (
	"crypto/md5"
	"encoding/binary"
	"sort"
	"strings"

	pb "github.com/lni/dragonboat/v3/raftpb"
	"github.com/lni/goutils/logutil"
)

func addressEqual(addr1 string, addr2 string) bool {
	return strings.EqualFold(strings.TrimSpace(addr1),
		strings.TrimSpace(addr2))
}

func deepCopyMembership(m pb.Membership) pb.Membership {
	c := pb.Membership{
		ConfigChangeId: m.ConfigChangeId,
		Addresses:      make(map[uint64]string),
		Removed:        make(map[uint64]bool),
		Observers:      make(map[uint64]string),
		Witnesses:      make(map[uint64]string),
	}
	for nid, addr := range m.Addresses {
		c.Addresses[nid] = addr
	}
	for nid := range m.Removed {
		c.Removed[nid] = true
	}
	for nid, addr := range m.Observers {
		c.Observers[nid] = addr
	}
	for nid, addr := range m.Witnesses {
		c.Witnesses[nid] = addr
	}
	return c
}

type membership struct {
	clusterID uint64
	nodeID    uint64
	ordered   bool
	members   *pb.Membership
}

func newMembership(clusterID uint64, nodeID uint64, ordered bool) *membership {
	return &membership{
		clusterID: clusterID,
		nodeID:    nodeID,
		ordered:   ordered,
		members: &pb.Membership{
			Addresses: make(map[uint64]string),
			Observers: make(map[uint64]string),
			Removed:   make(map[uint64]bool),
			Witnesses: make(map[uint64]string),
		},
	}
}

func (m *membership) id() string {
	return logutil.DescribeSM(m.clusterID, m.nodeID)
}

func (m *membership) set(n pb.Membership) {
	cm := deepCopyMembership(n)
	m.members = &cm
}

func (m *membership) get() (map[uint64]string,
	map[uint64]string, map[uint64]string, map[uint64]struct{}, uint64) {
	members := make(map[uint64]string)
	observers := make(map[uint64]string)
	removed := make(map[uint64]struct{})
	witnesses := make(map[uint64]string)
	for nid, addr := range m.members.Addresses {
		members[nid] = addr
	}
	for nid, addr := range m.members.Observers {
		observers[nid] = addr
	}
	for nid := range m.members.Removed {
		removed[nid] = struct{}{}
	}
	for nid, addr := range m.members.Witnesses {
		witnesses[nid] = addr
	}
	return members, observers, witnesses, removed, m.members.ConfigChangeId
}

func (m *membership) getMembership() pb.Membership {
	return deepCopyMembership(*m.members)
}

func (m *membership) getHash() uint64 {
	vals := make([]uint64, 0)
	for v := range m.members.Addresses {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	vals = append(vals, m.members.ConfigChangeId)
	data := make([]byte, 8)
	hash := md5.New()
	for _, v := range vals {
		binary.LittleEndian.PutUint64(data, v)
		if _, err := hash.Write(data); err != nil {
			panic(err)
		}
	}
	md5sum := hash.Sum(nil)
	return binary.LittleEndian.Uint64(md5sum[:8])
}

func (m *membership) isEmpty() bool {
	return len(m.members.Addresses) == 0
}

func (m *membership) isConfChangeUpToDate(cc pb.ConfigChange) bool {
	if !m.ordered || cc.Initialize {
		return true
	}
	if m.members.ConfigChangeId == cc.ConfigChangeId {
		return true
	}
	return false
}

func (m *membership) isAddingRemovedNode(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddNode ||
		cc.Type == pb.AddObserver ||
		cc.Type == pb.AddWitness {
		_, ok := m.members.Removed[cc.NodeID]
		return ok
	}
	return false
}

func (m *membership) isPromotingObserver(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddNode {
		oa, ok := m.members.Observers[cc.NodeID]
		return ok && addressEqual(oa, string(cc.Address))
	}
	return false
}

func (m *membership) isInvalidObserverPromotion(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddNode {
		oa, ok := m.members.Observers[cc.NodeID]
		return ok && !addressEqual(oa, string(cc.Address))
	}
	return false
}

func (m *membership) isAddingExistingMember(cc pb.ConfigChange) bool {
	// try to add again with the same node ID
	if cc.Type == pb.AddNode {
		_, ok := m.members.Addresses[cc.NodeID]
		if ok {
			return true
		}
	}
	if cc.Type == pb.AddObserver {
		_, ok := m.members.Observers[cc.NodeID]
		if ok {
			return true
		}
	}
	if cc.Type == pb.AddWitness {
		_, ok := m.members.Witnesses[cc.NodeID]
		if ok {
			return true
		}
	}
	if m.isPromotingObserver(cc) {
		return false
	}
	if cc.Type == pb.AddNode ||
		cc.Type == pb.AddObserver ||
		cc.Type == pb.AddWitness {
		for _, addr := range m.members.Addresses {
			if addressEqual(addr, string(cc.Address)) {
				return true
			}
		}
		for _, addr := range m.members.Observers {
			if addressEqual(addr, string(cc.Address)) {
				return true
			}
		}
		for _, addr := range m.members.Witnesses {
			if addressEqual(addr, string(cc.Address)) {
				return true
			}
		}
	}
	return false
}

func (m *membership) isAddingNodeAsObserver(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddObserver {
		_, ok := m.members.Addresses[cc.NodeID]
		return ok
	}
	return false
}
func (m *membership) isAddingNodeAsWitness(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddWitness {
		_, ok := m.members.Addresses[cc.NodeID]
		return ok
	}
	return false
}

func (m *membership) isAddingWitnessAsObserver(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddObserver {
		_, ok := m.members.Witnesses[cc.NodeID]
		return ok
	}
	return false
}

func (m *membership) isAddingWitnessAsNode(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddNode {
		_, ok := m.members.Witnesses[cc.NodeID]
		return ok
	}
	return false
}

func (m *membership) isAddingObserverAsWitness(cc pb.ConfigChange) bool {
	if cc.Type == pb.AddWitness {
		_, ok := m.members.Observers[cc.NodeID]
		return ok
	}
	return false
}

func (m *membership) isDeletingOnlyNode(cc pb.ConfigChange) bool {
	if cc.Type == pb.RemoveNode && len(m.members.Addresses) == 1 {
		_, ok := m.members.Addresses[cc.NodeID]
		return ok
	}
	return false
}

func (m *membership) applyConfigChange(cc pb.ConfigChange, index uint64) {
	m.members.ConfigChangeId = index
	switch cc.Type {
	case pb.AddNode:
		nodeAddr := string(cc.Address)
		delete(m.members.Observers, cc.NodeID)
		if _, ok := m.members.Witnesses[cc.NodeID]; ok {
			panic("not suppose to reach here")
		}
		m.members.Addresses[cc.NodeID] = nodeAddr
	case pb.AddObserver:
		if _, ok := m.members.Addresses[cc.NodeID]; ok {
			panic("not suppose to reach here")
		}
		m.members.Observers[cc.NodeID] = string(cc.Address)
	case pb.AddWitness:
		if _, ok := m.members.Addresses[cc.NodeID]; ok {
			panic("not suppose to reach here")
		}
		if _, ok := m.members.Observers[cc.NodeID]; ok {
			panic("not suppose to reach here")
		}
		m.members.Witnesses[cc.NodeID] = string(cc.Address)
	case pb.RemoveNode:
		delete(m.members.Addresses, cc.NodeID)
		delete(m.members.Observers, cc.NodeID)
		delete(m.members.Witnesses, cc.NodeID)
		m.members.Removed[cc.NodeID] = true
	default:
		panic("unknown config change type")
	}
}

var nid = logutil.NodeID

func (m *membership) handleConfigChange(cc pb.ConfigChange, index uint64) bool {
	// order id requested by user
	ccid := cc.ConfigChangeId
	nodeBecomingObserver := m.isAddingNodeAsObserver(cc)
	nodeBecomingWitness := m.isAddingNodeAsWitness(cc)
	witnessBecomingNode := m.isAddingWitnessAsNode(cc)
	witnessBecomingObserver := m.isAddingWitnessAsObserver(cc)
	observerBecomingWitness := m.isAddingObserverAsWitness(cc)
	alreadyMember := m.isAddingExistingMember(cc)
	addRemovedNode := m.isAddingRemovedNode(cc)
	upToDateCC := m.isConfChangeUpToDate(cc)
	deleteOnlyNode := m.isDeletingOnlyNode(cc)
	invalidPromotion := m.isInvalidObserverPromotion(cc)
	accepted := upToDateCC &&
		!addRemovedNode &&
		!alreadyMember &&
		!nodeBecomingObserver &&
		!nodeBecomingWitness &&
		!witnessBecomingNode &&
		!witnessBecomingObserver &&
		!observerBecomingWitness &&
		!deleteOnlyNode &&
		!invalidPromotion
	if accepted {
		// current entry index, it will be recorded as the conf change id of the members
		m.applyConfigChange(cc, index)
		if cc.Type == pb.AddNode {
			plog.Infof("%s applied ADD ccid %d (%d), %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), string(cc.Address))
		} else if cc.Type == pb.RemoveNode {
			plog.Infof("%s applied REMOVE ccid %d (%d), %s",
				m.id(), ccid, index, nid(cc.NodeID))
		} else if cc.Type == pb.AddObserver {
			plog.Infof("%s applied ADD OBSERVER ccid %d (%d), %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), string(cc.Address))
		} else if cc.Type == pb.AddWitness {
			plog.Infof("%s applied ADD WITNESS ccid %d (%d), %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), string(cc.Address))
		} else {
			plog.Panicf("unknown cc.Type value %d", cc.Type)
		}
	} else {
		if !upToDateCC {
			plog.Warningf("%s rej out-of-order ConfChange ccid %d (%d), type %s",
				m.id(), ccid, index, cc.Type)
		} else if addRemovedNode {
			plog.Warningf("%s rej add removed ccid %d (%d), %s",
				m.id(), ccid, index, nid(cc.NodeID))
		} else if alreadyMember {
			plog.Warningf("%s rej add exist ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else if nodeBecomingObserver {
			plog.Warningf("%s rej add exist as observer ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else if nodeBecomingWitness {
			plog.Warningf("%s rej add exist as witness ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else if witnessBecomingNode {
			plog.Warningf("%s rej add witness as node ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else if witnessBecomingObserver {
			plog.Warningf("%s rej add witness as observer ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else if observerBecomingWitness {
			plog.Warningf("%s rej add observer as witness ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else if deleteOnlyNode {
			plog.Warningf("%s rej remove the only node %s", m.id(), nid(cc.NodeID))
		} else if invalidPromotion {
			plog.Warningf("%s rej invalid observer promotion ccid %d (%d) %s (%s)",
				m.id(), ccid, index, nid(cc.NodeID), cc.Address)
		} else {
			plog.Panicf("config change rejected for unknown reasons")
		}
	}
	return accepted
}
