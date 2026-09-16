package fastfix

import (
	"os"
	"path/filepath"
	"testing"
)

// openTemplates loads the synthetic composed template set in testdata.
func openTemplates(t *testing.T) *Registry {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "composed.xml"))
	if err != nil {
		t.Fatalf("open templates: %v", err)
	}
	defer f.Close()
	reg, err := LoadTemplates(f)
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	return reg
}

func fieldsByName(t *Template) map[string]*Field {
	m := map[string]*Field{}
	for _, f := range t.Fields {
		m[f.Name] = f
	}
	return m
}

// TestLoadComposedTemplates covers the reference-composed style: messages
// built from shared <templateRef> blocks, annotated with <typeRef>, using the
// tail operator, and nesting a templateRef inside a sequence.
func TestLoadComposedTemplates(t *testing.T) {
	reg := openTemplates(t)

	snap := reg.Get(18)
	if snap == nil {
		t.Fatal("template 18 (Snapshot) not registered")
	}
	inc := reg.Get(20)
	if inc == nil {
		t.Fatal("template 20 (IncRefresh) not registered")
	}

	// SendingTime is inlined through two levels of templateRef
	// (IncRefresh -> Header -> HeaderBase) and keeps its tail operator.
	incFields := fieldsByName(inc)
	st, ok := incFields["SendingTime"]
	if !ok {
		t.Fatal("SendingTime not inlined into IncRefresh")
	}
	if st.Operator != OpTail {
		t.Errorf("SendingTime operator = %v, want OpTail", st.Operator)
	}

	// A templateRef inside a sequence contributes the element fields.
	seq, ok := incFields["NoEntries"]
	if !ok || seq.Kind != KindSequence {
		t.Fatal("NoEntries sequence missing from IncRefresh")
	}
	if seq.Length == nil || seq.Length.Name != "NoEntriesLen" {
		t.Errorf("NoEntries length field = %+v, want explicit NoEntriesLen", seq.Length)
	}
	elems := fieldsByName(&Template{Fields: seq.Fields})
	for _, want := range []string{"UpdateAction", "EntryType", "EntryPrice", "EntrySize"} {
		if _, ok := elems[want]; !ok {
			t.Errorf("EntryBlock field %q not expanded into NoEntries", want)
		}
	}

	// Message-level scalars sit alongside the inlined header.
	snapFields := fieldsByName(snap)
	if _, ok := snapFields["RefFlag"]; !ok {
		t.Error("RefFlag missing from Snapshot")
	}
	if s, ok := snapFields["SnapshotEntries"]; !ok || s.Kind != KindSequence {
		t.Error("SnapshotEntries sequence missing from Snapshot")
	}
}

// TestTypeRefIgnored pins that <typeRef> annotations never become fields.
func TestTypeRefIgnored(t *testing.T) {
	reg := openTemplates(t)
	for _, id := range []int{1, 7, 18, 20} {
		for _, f := range reg.Get(id).Fields {
			if f.Name == "MDEntry" || f.Name == "Logon" || f.Name == "MarketDataRequest" {
				t.Errorf("template %d: typeRef leaked into fields as %q", id, f.Name)
			}
		}
	}
}

// TestBuildingBlocksNotRegistered pins that id-less templates are available
// for expansion but are not decodable messages.
func TestBuildingBlocksNotRegistered(t *testing.T) {
	reg := openTemplates(t)
	for id, tpl := range reg.byID {
		switch tpl.Name {
		case "Header", "HeaderBase", "EntryBlock":
			t.Errorf("reference-only template %q registered at id %d", tpl.Name, id)
		}
	}
}

// TestOperatorParsing covers every operator the loader accepts, including
// initial values.
func TestOperatorParsing(t *testing.T) {
	reg := openTemplates(t)
	ops := reg.Get(30)
	if ops == nil {
		t.Fatal("template 30 not registered")
	}
	f := fieldsByName(ops)
	for name, want := range map[string]Op{
		"Const": OpConstant, "Copied": OpCopy, "Defaulted": OpDefault,
		"Seq": OpIncrement, "Delta": OpDelta, "Price": OpDefault,
	} {
		got, ok := f[name]
		if !ok {
			t.Errorf("field %q missing", name)
			continue
		}
		if got.Operator != want {
			t.Errorf("%s operator = %v, want %v", name, got.Operator, want)
		}
	}
	if c := f["Const"]; c != nil && (!c.HasInitial || c.InitInt != 7) {
		t.Errorf("Const initial = %d (has=%v), want 7", c.InitInt, c.HasInitial)
	}
	if p := f["Price"]; p != nil && p.InitDec.String() != "107.15" {
		t.Errorf("Price initial = %s, want 107.15", p.InitDec)
	}
}

// TestRegisterReset covers the programmatic reset-template registration that
// feeds typically require (the reset template is not in the XML).
func TestRegisterReset(t *testing.T) {
	reg := openTemplates(t)
	reg.RegisterReset(120, "FastResetTemplate")
	rst := reg.Get(120)
	if rst == nil || !rst.Reset {
		t.Fatal("reset template 120 not registered")
	}
}
