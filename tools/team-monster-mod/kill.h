// One-shot native damage call, owned by the EXE. No DLL or persistent hook.
// The generated Win32 thread has an SEH guard and rechecks the round/actors.
struct GuardWord { U address,value; };
struct KillCall {
    U source=0,target=0,send=0,damage=0,bits=0;
    bool poison=false;
    U state=0,health=0,notify=0,sourceNotify=0;
    BYTE packet[94]{};
    U effect[8]{};
    std::vector<GuardWord> guards;
};
struct Code32 {
    std::vector<BYTE> b;
    void bytes(std::initializer_list<BYTE> v){b.insert(b.end(),v);}
    void word(U v){size_t p=b.size();b.resize(p+4);memcpy(b.data()+p,&v,4);}
    size_t hole(){size_t p=b.size();word(0);return p;}
    void patch(size_t p,U v){memcpy(b.data()+p,&v,4);}
    void rel(size_t p,size_t target){patch(p,(U)(target-p-4));}
    void push(U v){bytes({0x68});word(v);}
    void call(U address){bytes({0xB8});word(address);bytes({0xFF,0xD0});}
    void status(U address,U v){bytes({0xC7,0x05});word(address);word(v);}
};
std::vector<BYTE> killCode(U code,U data,const KillCall& k) {
    Code32 c;std::vector<size_t> stale;
    c.bytes({0x55,0x8B,0xEC,0x60,0x83,0xEC,0x08}); // frame, pushad, SEH node
    c.bytes({0xC7,0x44,0x24,0x04});size_t handler=c.hole();
    c.bytes({0x64,0xA1,0,0,0,0,0x89,0x04,0x24,0x64,0x89,0x25,0,0,0,0});
    for(auto g:k.guards){c.bytes({0x81,0x3D});c.word(g.address);c.word(g.value);c.bytes({0x0F,0x85});stale.push_back(c.hole());}
    if(k.health) {
        c.bytes({0xB9});c.word(k.target);c.call(k.health);
        c.bytes({0xD9,0x1D});c.word(data+140);c.bytes({0xA1});c.word(data+140);
        c.bytes({0x85,0xC0,0x0F,0x8E});stale.push_back(c.hole());
        c.bytes({0x3D});c.word(0x7F800000);c.bytes({0x0F,0x83});stale.push_back(c.hole());
    }
    if(k.poison) {
        c.push(1);c.push(data+160);c.bytes({0xB9});c.word(k.state);c.call(k.damage);
        c.bytes({0x85,0xC0,0x0F,0x84});stale.push_back(c.hole());
    }
    c.push(0);c.push(0);c.push(k.poison?87:94);c.push(data);c.push(k.poison?8150:8121);c.call(k.send);c.bytes({0x83,0xC4,0x14});
    // Recheck after send: a concurrent room transition must not touch an old actor.
    for(auto g:k.guards){c.bytes({0x81,0x3D});c.word(g.address);c.word(g.value);c.bytes({0x0F,0x85});stale.push_back(c.hole());}
    size_t rejected=0;
    if(k.poison) {
        c.push(data+160);c.bytes({0xB9});c.word(k.state);c.call(k.notify);
        c.push(1);c.push(k.source);c.bytes({0xB9});c.word(k.target);c.call(k.sourceNotify);
    } else {
        c.push(0);c.push(1);c.push(0);c.push(k.target);c.push(k.source);c.push(k.bits);
        c.bytes({0xB9});c.word(k.target);c.call(k.damage); // thiscall, callee pops 24 bytes
        c.bytes({0x83,0xF8,0xFF,0x0F,0x84});rejected=c.hole();
    }
    c.status(data+128,1);c.bytes({0xE9});size_t success=c.hole();
    size_t skip=c.b.size();c.status(data+128,2);c.bytes({0xE9});size_t skipped=c.hole();
    size_t reject=c.b.size();c.status(data+128,4);c.bytes({0xE9});size_t rejectJump=c.hole();
    size_t fault=c.b.size();c.status(data+128,3);
    size_t cleanup=c.b.size();
    c.bytes({0x8B,0x04,0x24,0x64,0xA3,0,0,0,0,0x83,0xC4,0x08,0x61,0x33,0xC0,0x8B,0xE5,0x5D,0xC2,0x04,0});
    c.patch(handler,code+(U)c.b.size());
    // EXCEPTION_DISPOSITION handler(record, registration, CONTEXT*, dispatcher).
    // Resume at guarded cleanup with ESP at our registration record.
    c.bytes({0x8B,0x44,0x24,0x0C,0x8B,0x54,0x24,0x08,0x89,0x90});c.word(0xC4);
    c.bytes({0xC7,0x80});c.word(0xB8);c.word(code+(U)fault);c.bytes({0x33,0xC0,0xC3});
    for(size_t p:stale)c.rel(p,skip);
    if(rejected)c.rel(rejected,reject);c.rel(success,cleanup);c.rel(skipped,cleanup);c.rel(rejectJump,cleanup);
    return c.b;
}
struct KillJob {
    HANDLE thread=nullptr;U block=0;ULONGLONG started=0;
    bool start(HANDLE process,const KillCall& k) {
        if(thread)return false;
        block=(U)VirtualAllocEx(process,nullptr,8192,MEM_COMMIT|MEM_RESERVE,PAGE_READWRITE);
        if(!block)return false;
        auto code=killCode(block,block+4096,k);SIZE_T n=0;DWORD previous;
        bool ok=code.size()<=4096 && WriteProcessMemory(process,(void*)block,code.data(),code.size(),&n)&&n==code.size()&&
          WriteProcessMemory(process,(void*)(block+4096),k.packet,sizeof(k.packet),&n)&&n==sizeof(k.packet)&&
          WriteProcessMemory(process,(void*)(block+4096+160),k.effect,sizeof(k.effect),&n)&&n==sizeof(k.effect)&&
          VirtualProtectEx(process,(void*)block,4096,PAGE_EXECUTE_READ,&previous)&&FlushInstructionCache(process,(void*)block,code.size());
        if(ok)thread=CreateRemoteThread(process,nullptr,0,(LPTHREAD_START_ROUTINE)block,nullptr,0,nullptr);
        if(!thread){VirtualFreeEx(process,(void*)block,0,MEM_RELEASE);block=0;return false;}
        started=GetTickCount64();return true;
    }
    // Only free executable memory after the thread exits. No TerminateThread.
    int poll(HANDLE process) {
        if(!thread)return -2;
        if(WaitForSingleObject(thread,0)!=WAIT_OBJECT_0)return -1;
        U result=0;read(process,block+4096+128,result);
        CloseHandle(thread);thread=nullptr;VirtualFreeEx(process,(void*)block,0,MEM_RELEASE);block=0;
        return (int)result;
    }
} killJob;
HWND killButton=nullptr,poisonCheck=nullptr,killStatus=nullptr;
struct WorkTarget {Actor actor;bool poison;};
std::vector<WorkTarget> killQueue;size_t killNext=0;unsigned killDone=0,killSkipped=0;bool killCancel=false;
bool autoFault=false,autoPaused=false;
ULONGLONG lastRound=0;
struct PoisonStamp {Actor actor;ULONGLONG when;};
std::vector<PoisonStamp> poisonStamps;
WorkTarget currentTarget{};
bool checked(HWND h){return h&&SendMessageW(h,BM_GETCHECK,0,0)==BST_CHECKED;}
bool isNpc(const Actor& a,U* table=nullptr) {
    U vt=0,col=0,type=0;
    if(!read(session.process,a.ptr,vt)||!read(session.process,vt-4,col)||!read(session.process,col+12,type))return false;
    if(table)*table=vt;
    // CNpc RTTI type descriptor from this exact gfxz.dat; excludes players/pets.
    return type==session.base+0xEF6B00;
}
bool poisonDue(const Actor& a,ULONGLONG now) {
    for(const auto& s:poisonStamps)if(s.actor.ptr==a.ptr&&s.actor.state==a.state&&s.actor.uid==a.uid)return now-s.when>=4000;
    return true;
}
void stampPoison(const Actor& a,ULONGLONG now) {
    for(auto& s:poisonStamps)if(s.actor.uid==a.uid){s={a,now};return;}
    if(poisonStamps.size()>=128)poisonStamps.erase(poisonStamps.begin());
    poisonStamps.push_back({a,now});
}
bool listActors(HANDLE h,U manager,std::vector<Actor>& result) {
    U head=0,root=0;if(!read(h,manager+0x2F0,head)||!read(h,head+4,root))return false;
    std::vector<U> todo{root};std::set<U> seen;
    while(!todo.empty()&&seen.size()<128) {
        U node=todo.back();todo.pop_back();if(!node||node==head||!seen.insert(node).second)continue;
        struct Node {U left,parent,right,key,value;BYTE color,nil,pad[2];} n{};
        if(!read(h,node,n))return false;if(n.nil)continue;todo.push_back(n.left);todo.push_back(n.right);
        Actor a;a.ptr=n.value;
        if(n.key<40 && a.ptr && read(h,a.ptr+0xC90,a.uid)&&a.uid&&read(h,a.ptr+0x1A6C,a.state)&&a.state)result.push_back(a);
    }
    return todo.empty();
}
bool makeKill(const Actor& enemy,KillCall& k,bool poison=false) {
    Actor own=session.actor;U manager=0,ownTeam=0,enemyTeam=0,watch=0,action=0;
    if(!session.same()||!own.state||enemy.uid==own.uid||!enemy.state||
       !read(session.process,own.ptr+0x7C,watch)||watch!=1||
       !read(session.process,enemy.ptr+0x7C,watch)||watch!=1||
       !read(session.process,own.ptr+0xCE0,action)||!action||
       !read(session.process,enemy.ptr+0xCE0,action)||!action||
       !read(session.process,own.ptr+0x28,ownTeam)||!read(session.process,enemy.ptr+0x28,enemyTeam)||(!poison&&ownTeam==enemyTeam)||
       !read(session.process,session.base+0x13C8708,manager))return false;
    // Native 432440 returns manager+0x311 (BYTE offset, not 0xC44).
    uint64_t context=0;if(!read(session.process,manager+0x311,context)||!context)return false;
    k.source=own.ptr;k.target=enemy.ptr;k.send=session.base+0x63FBB0;k.damage=session.base+0x5F29F0;k.health=session.base+0x5E45B0;
    float damage=9999.0f;memcpy(&k.bits,&damage,4);
    memcpy(k.packet+4,&own.uid,8);memcpy(k.packet+39,&enemy.uid,8);memcpy(k.packet+47,&own.uid,8);
    k.packet[65]=1;memcpy(k.packet+67,&k.bits,4);memcpy(k.packet+86,&context,8);
    U targetAction=0;if(!read(session.process,enemy.ptr+0xCE4,targetAction))return false;k.packet[55]=(BYTE)targetAction;
    k.guards={{session.base+0x13C8708,manager},{manager+0x311,(U)context},{manager+0x315,(U)(context>>32)}};
    for(auto a:{own,enemy}) {
        k.guards.push_back({a.ptr+0xC90,(U)a.uid});k.guards.push_back({a.ptr+0xC94,(U)(a.uid>>32)});
        k.guards.push_back({a.ptr+0x1A6C,a.state});k.guards.push_back({a.ptr+0x7C,1});
    }
    k.guards.push_back({own.ptr+0x28,ownTeam});k.guards.push_back({enemy.ptr+0x28,enemyTeam});
    if(poison) {
        U vt=0;if(!isNpc(enemy,&vt))return false;
        k.poison=true;k.state=enemy.state;k.damage=session.base+0x5C69C0;k.notify=session.base+0x5C5580;k.sourceNotify=session.base+0x5E7480;
        memset(k.packet,0,sizeof(k.packet));
        memcpy(k.packet+4,&own.uid,8);memcpy(k.packet+39,&enemy.uid,8);memcpy(k.packet+47,&own.uid,8);
        U code=1,level=1,duration=3000,operation=1;
        memcpy(k.packet+55,&code,4);memcpy(k.packet+59,&level,4);memcpy(k.packet+63,&duration,4);memcpy(k.packet+75,&operation,4);memcpy(k.packet+79,&context,8);
        k.effect[0]=code;k.effect[1]=level;k.effect[2]=duration;k.effect[4]=(U)own.uid;k.effect[5]=(U)(own.uid>>32);k.effect[6]=1;
        U weaponSlot=0,weapon=0,weaponInfo=0;
        if(!read(session.process,enemy.ptr+0xCB0,weaponSlot)||!read(session.process,enemy.ptr+(weaponSlot?0xCC0:0xCC4),weapon))return false;
        if(weapon&&!read(session.process,weapon+0x37C,weaponInfo))return false;k.effect[7]=weaponInfo;
        k.guards.push_back({enemy.ptr,vt});k.guards.push_back({enemy.state,enemy.ptr});
    }
    // Check function prologues as well as the on-disk hash used at connect.
    U prologue=0;
    return read(session.process,k.send,prologue)&&prologue==0x51EC8B55&&read(session.process,k.health,prologue)&&prologue==0x81EC8B55&&
      read(session.process,k.damage,prologue)&&prologue==(poison?0x81EC8B55:0x83EC8B55)&&
      (!poison||(read(session.process,k.notify,prologue)&&prologue==0x83EC8B55&&read(session.process,k.sourceNotify,prologue)&&prologue==0x51EC8B55));
}
bool killBusy(){return killJob.thread||killNext<killQueue.size();}
void pollKill() {
    if(!killBusy())return;
    if(killJob.thread) {
        int result=killJob.poll(session.process);
        if(result==-1){if(GetTickCount64()-killJob.started>5000){killCancel=true;autoFault=true;SetWindowTextW(killStatus,L"调用超时：自动操作已暂停，等待当前调用返回；不强杀线程。");}return;}
        if(currentTarget.poison&&(result==1||result==2))stampPoison(currentTarget.actor,GetTickCount64());
        if(result==1)++killDone;else {++killSkipped;if(result!=2){killCancel=true;autoFault=true;}}
    }
    while(!killCancel && killNext<killQueue.size()) {
        currentTarget=killQueue[killNext++];
        if(!checked(currentTarget.poison?poisonCheck:killButton)){++killSkipped;continue;}
        KillCall k;if(!makeKill(currentTarget.actor,k,currentTarget.poison)){++killSkipped;continue;}
        if(!killJob.start(session.process,k)){killCancel=true;autoFault=true;++killSkipped;break;}
        return;
    }
    killQueue.clear();killNext=0;
    if(autoFault){SetWindowTextW(killStatus,L"自动操作因调用失败/异常暂停。勾选已保留；检查后重新连接恢复。");return;}
    wchar_t text[220];swprintf_s(text,L"%s：调用完成 %u，跳过/失败 %u。请在双方游戏中确认效果。",killCancel?L"已停止":L"本次结束",killDone,killSkipped);SetWindowTextW(killStatus,text);
}
void startKill() {
    if(killBusy()||autoFault||autoPaused)return;
    U manager=0;std::vector<Actor> actors;
    if(!session.process||!session.actor.state||!session.same()||!read(session.process,session.base+0x13C8708,manager)||!listActors(session.process,manager,actors)) {
        SetWindowTextW(killStatus,L"请先连接，并进入有敌方角色的战斗。");return;
    }
    killQueue.clear();killNext=killDone=killSkipped=0;killCancel=false;
    for(const auto& a:actors) {
        if(checked(poisonCheck)&&poisonDue(a,GetTickCount64())){KillCall k;if(makeKill(a,k,true))killQueue.push_back({a,true});}
        if(checked(killButton)){KillCall k;if(makeKill(a,k))killQueue.push_back({a,false});}
    }
    if(killQueue.empty()){SetWindowTextW(killStatus,L"已勾选，等待可操作目标；NPC 中毒间隔 4 秒补一次。");return;}
    SetWindowTextW(killStatus,L"正在执行；上一轮未结束时不重复创建任务。");pollKill();
}
bool roundDue(ULONGLONG now,ULONGLONG previous,bool busy,bool paused,bool wanted) {
    return !busy&&!paused&&wanted&&now-previous>=400;
}
void autoTick() {
    pollKill();
    ULONGLONG now=GetTickCount64();
    if(roundDue(now,lastRound,killBusy(),autoFault||autoPaused,checked(killButton)||checked(poisonCheck))) {
        lastRound=now;startKill();
    }
}

// Executed only by --self-test: real generated code, fake native functions.
static U fakeSendCount=0,fakeDamageCount=0;
static U fakeApplyCount=0,fakeNotifyCount=0,fakeSourceCount=0;
static bool fakeApplyAccept=true;
static float fakeHP=100;
static KillCall fakeExpected;
void __cdecl fakeSend(U id,BYTE* packet,U length,U a,U b) {
    if(id==(fakeExpected.poison?8150u:8121u)&&length==(fakeExpected.poison?87u:94u)&&!a&&!b&&!memcmp(packet,fakeExpected.packet,length))++fakeSendCount;
}
void __cdecl fakeSlowSend(U id,BYTE* packet,U length,U a,U b){Sleep(80);fakeSend(id,packet,length,a,b);}
float __fastcall fakeHealth(void*,void*){return fakeHP;}
U __fastcall fakeApply(void* state,void*,const U* effect,U flag) {
    if((U)state==fakeExpected.state&&flag==1&&!memcmp(effect,fakeExpected.effect,32))++fakeApplyCount;
    return fakeApplyAccept?1:0;
}
U __fastcall fakeNotify(void* state,void*,const U* effect) {
    if((U)state==fakeExpected.state&&!memcmp(effect,fakeExpected.effect,32))++fakeNotifyCount;return 0;
}
U __fastcall fakeSource(void* target,void*,U source,U code) {
    if((U)target==fakeExpected.target&&source==fakeExpected.source&&code==1)++fakeSourceCount;return 0;
}
U __fastcall fakeDamage(void* target,void*,U damage,U source,U victim,U zero,U one,U end) {
    if((U)target==fakeExpected.target&&damage==fakeExpected.bits&&source==fakeExpected.source&&victim==fakeExpected.target&&!zero&&one==1&&!end)++fakeDamageCount;
    return 0;
}
bool killSelfTest() {
    U guard=123;KillCall k;k.source=10;k.target=20;k.bits=0x461C3C00;k.send=(U)&fakeSend;k.damage=(U)&fakeDamage;k.health=(U)&fakeHealth;k.packet[65]=1;
    k.guards={{(U)&guard,123}};fakeExpected=k;fakeSendCount=fakeDamageCount=0;
    auto run=[&](const KillCall& call){KillJob j;if(!j.start(GetCurrentProcess(),call))return -10;
        if(WaitForSingleObject(j.thread,3000)!=WAIT_OBJECT_0)return -11;
        return j.poll(GetCurrentProcess());};
    bool ok=run(k)==1&&fakeSendCount==1&&fakeDamageCount==1;
    guard=124;ok=ok&&run(k)==2&&fakeSendCount==1&&fakeDamageCount==1;
    guard=123;k.send=1;ok=ok&&run(k)==3&&fakeDamageCount==1;
    k.send=(U)&fakeSend;fakeHP=0;ok=ok&&run(k)==2&&fakeDamageCount==1;
    fakeHP=100;k.poison=true;k.state=30;k.damage=(U)&fakeApply;k.notify=(U)&fakeNotify;k.sourceNotify=(U)&fakeSource;
    k.effect[0]=k.effect[1]=1;k.effect[2]=3000;k.effect[4]=10;k.effect[6]=1;fakeExpected=k;
    ok=ok&&run(k)==1&&fakeApplyCount==1&&fakeNotifyCount==1&&fakeSourceCount==1&&fakeSendCount==2;
    fakeApplyAccept=false;ok=ok&&run(k)==2&&fakeNotifyCount==1&&fakeSendCount==2;fakeApplyAccept=true;
    k.send=(U)&fakeSlowSend;
    KillJob j;bool started=j.start(GetCurrentProcess(),k);ok=ok&&started;
    if(started){ok=ok&&!j.start(GetCurrentProcess(),k)&&j.poll(GetCurrentProcess())==-1;WaitForSingleObject(j.thread,3000);ok=ok&&j.poll(GetCurrentProcess())==1&&!j.thread&&!j.block;}
    ok=ok&&!roundDue(1399,1000,false,false,true)&&roundDue(1400,1000,false,false,true)&&!roundDue(1800,1000,true,false,true)&&!roundDue(1800,1000,false,true,true)&&!roundDue(1800,1000,false,false,false);
    poisonStamps.clear();Actor npc{123,456,789};stampPoison(npc,1000);
    ok=ok&&!poisonDue(npc,4999)&&poisonDue(npc,5000);npc.state=457;ok=ok&&poisonDue(npc,1001);poisonStamps.clear();
    return ok;
}
