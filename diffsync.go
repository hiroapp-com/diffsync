package diffsync


type Auther interface {
    Grant(string, string, *Resource)
}
type EventHandler interface {
   Inbox() chan<- Event 
}

    


type ResourceRegistry map[string]map[string]bool 

func (rr ResourceRegistry) Add(res *Resource) {
    if _, ok := rr[res.kind]; !ok {
        rr[res.kind] = make(map[string]bool)
    }
    rr[res.kind][res.id] = true
}

func (rr ResourceRegistry) Remove(res *Resource) {
    if _, ok := rr[res.kind]; ok {
        delete(rr[res.kind], res.id)
        if len(rr[res.kind]) == 0 {
            delete(rr, res.kind)
        }
    }
}

