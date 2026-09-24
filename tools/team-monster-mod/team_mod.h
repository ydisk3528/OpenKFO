#include <winhttp.h>
const char* monsterModels[]={"M_NPC_EGUN","M_NPC_GAODAO","M_NPC_BEAR"};
HWND monsterChoice=nullptr,monsterCount=nullptr,monsterAI=nullptr;
#pragma comment(lib,"winhttp.lib")
struct TeamStatus {U magic=0,serial=0;uint64_t owner=0;U ready=0,mode=0;};
bool teamHttp(const wchar_t* method,const wchar_t* path,void* output,DWORD length) {
    HINTERNET internet=WinHttpOpen(L"OpenKFO local MOD",WINHTTP_ACCESS_TYPE_NO_PROXY,nullptr,nullptr,0);
    if(!internet)return false;WinHttpSetTimeouts(internet,500,500,500,500);
    HINTERNET connection=WinHttpConnect(internet,L"127.0.0.1",19090,0);
    HINTERNET request=connection?WinHttpOpenRequest(connection,method,path,nullptr,nullptr,nullptr,0):nullptr;
    DWORD code=0,size=sizeof(code),received=0;bool ok=false;
    if(request&&WinHttpSendRequest(request,nullptr,0,nullptr,0,0,0)&&WinHttpReceiveResponse(request,nullptr)&&
       WinHttpQueryHeaders(request,WINHTTP_QUERY_STATUS_CODE|WINHTTP_QUERY_FLAG_NUMBER,nullptr,&code,&size,nullptr)&&code==(length?200U:204U)){
        BYTE* out=(BYTE*)output;DWORD total=0;
        while(total<length&&WinHttpReadData(request,out+total,length-total,&received)&&received)total+=received;
        ok=total==length;
    }
    if(request)WinHttpCloseHandle(request);if(connection)WinHttpCloseHandle(connection);WinHttpCloseHandle(internet);return ok;
}
bool getTeamStatus(U room,U serial,TeamStatus& reply) {
    wchar_t path[160];swprintf_s(path,L"/experimental/neutral-npc?room=%u&serial=%u",room,serial);
    return teamHttp(L"GET",path,&reply,sizeof(reply))&&reply.magic==0x3143504E&&reply.serial==serial&&(reply.mode==1||reply.mode==3);
}
struct TeamReplica {uint64_t uid=0;U state=0,error=0;};
struct TeamPlan {
    U magic=0,serial=0;uint64_t owner=0;U state=0,mode=0,npc=0,floor=0,position[3]{},count=0,room=0,monsters=0,model=0,ai=0;
    TeamReplica members[8];
};
static_assert(sizeof(TeamPlan)==192,"NPC3 layout");
bool validTeamPlan(const TeamPlan& p,U room,U serial) {
    if(p.magic!=0x3343504e||p.room!=room||p.serial!=serial||!p.owner||p.npc!=100||p.state>3||
       (p.mode!=1&&p.mode!=3)||p.count>8||(p.state&&(p.count==0||p.monsters<1||p.monsters>4||p.model>2||p.ai>1)))return false;
    std::set<uint64_t> seen;bool owner=false;
    for(U i=0;i<p.count;i++){auto m=p.members[i];if(!m.uid||m.state<1||m.state>3||!seen.insert(m.uid).second)return false;if(m.uid==p.owner)owner=true;}
    for(U bits:p.position){float v;memcpy(&v,&bits,4);if(!std::isfinite(v)||fabs(v)>100000)return false;}
    return !p.state||owner;
}
bool getTeamPlan(U room,U serial,TeamPlan& p) {
    wchar_t path[160];swprintf_s(path,L"/experimental/neutral-npc-plan?room=%u&serial=%u",room,serial);
    return teamHttp(L"GET",path,&p,sizeof(p))&&validTeamPlan(p,room,serial);
}
bool reportTeamFailure(U room,U serial,uint64_t uid,U code) {
    wchar_t path[200];swprintf_s(path,L"/experimental/neutral-npc-failure?room=%u&serial=%u&uid=%llu&code=%u",room,serial,(unsigned long long)uid,code);
    return teamHttp(L"POST",path,nullptr,0);
}
void sendTeamReady(Code32& c,U data,U room,U serial,U monsters,U model,U ai) {
    c.status(data+128+39,100);c.status(data+128+43,room);c.status(data+128+47,serial);
    c.status(data+128+51,monsters);c.status(data+128+55,model);c.status(data+128+59,ai);
    c.push(0);c.push(0);c.push(63);c.push(data+128);c.push(21900);c.call(session.base+0x63FBB0);c.bytes({0x83,0xC4,0x14});
}
// No PVE mode cast or training slot. Every replica is created without AI;
// the local server admits the NPC only after every fighter sends its receipt.
bool startTeamProbe(U expectedSerial,bool registerNPC=false) {
    Actor player;U scene=0,mode=0,vt=0,phase=0,manager=0,serial=0,room=0,floor=0;
    if(!session.current(player)||!read(session.process,session.base+0x13C8710,scene)||
       !read(session.process,scene+0xAC,mode)||!read(session.process,mode,vt)||
       (vt!=session.base+0x7A9144&&vt!=session.base+0x7A87D4)||
       !read(session.process,scene+0x78,phase)||phase!=4||
       !read(session.process,session.base+0x13C8708,manager)||
       !read(session.process,manager+0x311,room)||!read(session.process,manager+0x315,serial)||
       serial!=expectedSerial||!room||!read(session.process,player.ptr+0xD00,floor))return false;
    std::vector<Actor> actors;
    if(!listActors(session.process,manager,actors))return false;
    for(auto a:actors)if(isNpc(a))return false; // One probe per scene; never replace a live NPC.
    U position[3]{},monsters=1,modelIndex=0,aiIndex=0;
    if(registerNPC){
        TeamPlan plan;if(!getTeamPlan(room,serial,plan)||plan.state!=1)return false;
        bool admitted=false;for(U i=0;i<plan.count;i++)if(plan.members[i].uid==player.uid&&plan.members[i].state==1)admitted=true;
        if(!admitted)return false;
        floor=plan.floor;monsters=plan.monsters;modelIndex=plan.model;aiIndex=plan.ai;memcpy(position,plan.position,sizeof(position));
    } else for(U i=0;i<3;i++)if(!read(session.process,player.ptr+0xD10+i*4,position[i]))return false;
    for(U i=0;i<3;i++){
        float v=0;memcpy(&v,&position[i],4);if(!std::isfinite(v)||fabs(v)>100000)return false;
    }
    std::vector<GuardWord> guards={{session.base+0x13C8710,scene},{scene+0xAC,mode},{mode,vt},
        {scene+0x78,phase},{session.base+0x13C8708,manager},{manager+0x311,room},{manager+0x315,serial},
        {player.ptr+0xC90,(U)player.uid},{player.ptr+0xC94,(U)(player.uid>>32)}};
    for(auto g:std::vector<GuardWord>{{session.base+0x693550,0x6AEC8B55},{session.base+0x693730,0x6AEC8B55}}){
        U v=0;if(!read(session.process,g.address,v)||v!=g.value)return false;guards.push_back(g);
    }
    U base=session.base;
    return modJob.start(guards,[=](Code32& c,U data){
        const char* model=monsterModels[modelIndex];size_t modelSize=strlen(model)+1;
        for(U i=0;i<modelSize;i+=4){U v=0;memcpy(&v,model+i,(modelSize-i<4)?modelSize-i:4);c.status(data+64+i,v);}
        std::vector<size_t> failed;
        for(U index=0;index<monsters;index++) {
            U pos[3];memcpy(pos,position,sizeof(pos));float x;memcpy(&x,&pos[0],4);x+=((float)index-((float)monsters-1)/2)*20.0f;memcpy(&pos[0],&x,4);
            for(U i=0;i<3;i++)c.status(data+96+i*4,pos[i]);
            c.push(0);c.push(100+index);c.push(data+96);c.push(floor);c.push(data+64);
            c.call(base+0x693550);c.bytes({0x8B,0xC8});c.call(base+0x693730);
            c.bytes({0x85,0xC0,0x0F,0x84});failed.push_back(c.hole());
            c.bytes({0xA3});c.word(data+16);
            // All replicas stay passive. A partial batch never sends a ready receipt.
        }
        if(registerNPC)sendTeamReady(c,data,room,serial,monsters,modelIndex,aiIndex);
        c.status(data,3);c.bytes({0xE9});size_t done=c.hole();
        for(auto jump:failed)c.rel(jump,c.b.size());c.status(data,5);c.rel(done,c.b.size());
    });
}

bool startTeamController(U expectedSerial) {
    Actor player,npc;U scene=0,mode=0,vt=0,phase=0,manager=0,room=0,serial=0;
    if(!session.current(player)||!read(session.process,session.base+0x13C8710,scene)||
       !read(session.process,scene+0xAC,mode)||!read(session.process,mode,vt)||
       (vt!=session.base+0x7A9144&&vt!=session.base+0x7A87D4)||
       !read(session.process,scene+0x78,phase)||phase!=4||!read(session.process,session.base+0x13C8708,manager)||
       !read(session.process,manager+0x311,room)||!read(session.process,manager+0x315,serial)||serial!=expectedSerial||
       !resolve(session.process,manager,100,npc)||!isNpc(npc))return false;
    TeamPlan plan;if(!getTeamPlan(room,serial,plan)||plan.state!=2||plan.owner!=player.uid)return false;
    std::vector<Actor> npcs;
    std::vector<GuardWord> guards={{session.base+0x13C8710,scene},{scene+0xAC,mode},{mode,vt},{scene+0x78,phase},
        {session.base+0x13C8708,manager},{manager+0x311,room},{manager+0x315,serial}};
    for(U i=0;i<plan.monsters;i++) {
        Actor n;U emitting=0;if(!resolve(session.process,manager,100+i,n)||!isNpc(n)||!read(session.process,n.ptr+0xDD0,emitting)||emitting)return false;npcs.push_back(n);
        guards.push_back({n.ptr+0xC90,100+i});guards.push_back({n.ptr+0xC94,0});guards.push_back({n.ptr,session.base+0x7A174C});
        guards.push_back({n.ptr+0xDD0,0});
    }

    U base=session.base;
    return modJob.start(guards,[=](Code32& c,U data){
        char ai[]="pve008";if(plan.ai==1)ai[5]='6';
        for(U i=0;i<sizeof(ai);i+=4){U v=0;memcpy(&v,ai+i,(sizeof(ai)-i<4)?sizeof(ai)-i:4);c.status(data+64+i,v);}
        std::vector<size_t> failed;
        for(auto monster:npcs){
        c.push(data+64);c.bytes({0xB9});c.word(monster.ptr);c.call(base+0x6015A0);
        c.bytes({0x84,0xC0,0x0F,0x84});failed.push_back(c.hole());
        }
        // Load every AI first. Failed loading must not activate only part of a batch.
        for(auto monster:npcs){
        // 9E7C70 reads DD0 to decide whether this actor emits native events.
        c.status(monster.ptr+0xDD0,1);
        c.bytes({0xB9});c.word(monster.ptr);c.call(base+0x5FF460);
        }
        c.status(data,3);c.bytes({0xE9});size_t done=c.hole();
        for(auto jump:failed)c.rel(jump,c.b.size());c.status(data,5);c.rel(done,c.b.size());
    });
}

bool teamOperation=false;
bool requestTeamSpawn();
void startTeamUI(bool controller) {
    if(modJob.busy()||killBusy()||checked(killButton)||checked(poisonCheck)){
        SetWindowTextW(modStatus,L"请等当前操作完成，并取消秒杀和 NPC 中毒。");return;
    }
    U manager=0,serial=0;
    bool ok=read(session.process,session.base+0x13C8708,manager)&&read(session.process,manager+0x315,serial)&&
        (controller?startTeamController(serial):requestTeamSpawn());
    teamOperation=ok;
    SetWindowTextW(modStatus,ok?L"正在处理团队怪物，请保持本局打开。":controller?
        L"未执行：必须是房主，并且本局所有玩家都已生成并登记怪物。线下服务器须开启 MOD 实验。":
        L"未执行：请选择房主窗口；需要新版线下实验服务器、团队战地图、本局尚未生成。所有参战窗口需在本机。");
}
