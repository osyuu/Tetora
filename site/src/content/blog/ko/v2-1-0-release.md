---
title: "Tetora v2.1.0 — 대규모 코드베이스 통합 + Workflow Engine"
lang: ko
date: "2026-03-18"
tag: release
readTime: "약 5분"
excerpt: "256개 파일을 슬림한 코어로 통합. DAG 지원 신규 Workflow Engine과 Template Marketplace."
---

Tetora v2.1.0은 대규모 업데이트입니다. 이번 버전의 핵심 테마는 두 가지: **코드 아키텍처 대정리**와 **신기능 출시**입니다.

사용자 관점에서는 더 안정적인 실행 환경, 빠른 반복 주기, 그리고 오랫동안 기다려온 Workflow Engine과 Template Marketplace를 제공합니다. 개발자 관점에서는 Tetora가 빠른 프로토타입에서 장기적으로 유지 가능한 제품으로 나아가는 중요한 전환점입니다.

> **한 줄 요약:** root 소스 파일을 28개에서 9개로, 테스트 파일을 111개에서 22개로 통합. 전체 리포지토리를 256+개 파일에서 슬림한 구조로 압축——동시에 Workflow Engine, Template Marketplace, 다양한 Dashboard 개선 사항을 추가했습니다.

## 코드베이스 통합: 256개 파일 → 슬림한 코어

이번 통합은 여러 라운드의 리팩토링을 거쳐 다음과 같은 성과를 달성했습니다:

| 지표 | 통합 전 | 통합 후 |
|---|---|---|
| 리포지토리 총 파일 수 | 256+ | ~73 (진행 중) |
| Root 소스 파일 수 | 28 | 9 |
| 테스트 파일 수 | 111 | 22 |

9개의 root 파일은 도메인별로 분리됩니다:

- `main.go` — 진입점, 명령 라우팅, 시작 프로세스
- `http.go` — HTTP 서버, API 라우팅, Dashboard 핸들러
- `discord.go` — Discord 게이트웨이, 메시지 처리, 터미널 브리지
- `dispatch.go` — 태스크 디스패치, TaskBoard, 동시성 제어
- `workflow.go` — Workflow Engine, DAG 실행, 단계 관리
- `wire.go` — 크로스 모듈 배선, 초기화, 의존성 주입
- `tool.go` — 도구 시스템, MCP 통합, 기능 관리
- `signal_unix.go` / `signal_windows.go` — 플랫폼별 시그널 처리

많은 비즈니스 로직이 `internal/` 서브 패키지(`internal/cron`, `internal/dispatch`, `internal/workflow`, `internal/taskboard`, `internal/reflection` 등)로 이전되어, root 레이어는 얇은 조정 로직만 유지합니다.

### 왜 중요한가요?

이전에는 root 레이어에 100개 이상의 파일이 있어 기능이 분산되어 있었고, 새 기여자가 올바른 파일을 찾는 데만 상당한 시간이 필요했습니다. 통합 후:

- **유지 보수 용이** — 기능을 변경할 때 어디를 봐야 할지 명확
- **빠른 온보딩** — 28개가 아닌 9개의 진입점
- **명확한 IDE 내비게이션** — go to definition이 파일 더미에서 길을 잃지 않음
- **빌드 속도 향상** — 불필요한 패키지 경계와 import 체인 감소

## Workflow Engine

Workflow Engine은 v2.1.0의 핵심 신기능입니다. YAML로 멀티스텝 AI 워크플로를 정의하면, Tetora가 실행, 오류 처리, 상태 추적을 담당합니다.

### DAG 기반 파이프라인

워크플로는 방향성 비순환 그래프(DAG) 구조로 정의되며, 다음을 지원합니다:

- **조건 분기** — 이전 단계의 출력을 기반으로 실행 경로 결정
- **병렬 단계** — 의존성이 없는 단계를 동시에 실행하여 총 실행 시간 단축
- **재시도 메커니즘** — 단계 실패 시 자동 재시도, 횟수와 백오프 전략 설정 가능

```yaml
name: content-pipeline
steps:
  - id: research
    agent: hisui
    prompt: "주제 조사: {{input.topic}}"
  - id: draft
    agent: kokuyou
    depends_on: [research]
    prompt: "조사 결과를 바탕으로 초안 작성"
  - id: review
    agent: ruri
    depends_on: [draft]
    condition: "{{draft.word_count}} > 500"
```

### 다이나믹 모델 라우팅

Workflow Engine은 태스크 복잡도에 따라 자동으로 모델을 선택합니다:

- 간단한 포맷팅, 요약 → **Haiku** (빠름, 저비용)
- 일반 추론, 문장 작성 → **Sonnet** (기본값)
- 복잡한 분석, 멀티스텝 계획 → **Opus** (최고 성능)

YAML에서 명시적으로 지정하거나, 프롬프트 길이와 키워드를 기반으로 라우터가 자동 판단하게 할 수 있습니다.

### Dashboard DAG 시각화

실행 중인 워크플로는 Dashboard에 노드 그래프로 표시됩니다: 완료 단계는 녹색, 실행 중은 보라색 애니메이션, 대기 중은 회색, 실패는 빨간색. 로그를 확인하지 않아도 파이프라인 전체 진행 상황을 실시간으로 파악할 수 있습니다.

## Template Marketplace

Template Marketplace로 워크플로 템플릿을 공유, 탐색, 원클릭 임포트할 수 있게 되었습니다. Tetora가 개인 도구에서 커뮤니티 생태계로 나아가는 첫 걸음입니다.

### Store 탭

Dashboard에 새로운 Store 탭이 추가되어 다음을 제공합니다:

- **카테고리 탐색** — 도메인별 필터링 (마케팅, 엔지니어링, 재무, 리서치 등)
- **전체 텍스트 검색** — 템플릿 이름과 설명에서 검색
- **추천 섹션** — 공식 추천 고품질 템플릿
- **원클릭 임포트** — 클릭으로 로컬 workspace에 바로 임포트

### Capabilities 탭

새로운 Capabilities 탭에서 Tetora 인스턴스가 가진 모든 기능을 한눈에 볼 수 있습니다:

- **Tools** — 사용 가능한 MCP 도구 목록
- **Skills** — 정의된 Skill 명령어
- **Workflows** — 로컬 Workflow 템플릿
- **Templates** — Agent 프롬프트 템플릿

### CLI 임포트 / 익스포트

UI뿐만 아니라 CLI도 완전히 지원합니다:

```bash
tetora workflow export my-pipeline   # 공유 가능한 YAML로 익스포트
tetora workflow create from-store    # Store에서 템플릿 임포트
tetora workflow list                  # 로컬 Workflow 목록 표시
```

익스포트한 YAML은 GitHub Gist나 Tetora Store에 바로 붙여넣어 커뮤니티와 공유할 수 있습니다.

## TaskBoard & Dispatch 개선

TaskBoard와 Dispatch 레이어의 여러 중요한 개선으로 멀티 에이전트 병렬 작업의 안정성과 가시성이 향상되었습니다.

### 설정 가능한 병렬 슬롯 + 슬롯 프레셔

이제 설정 파일에서 최대 병렬 슬롯 수와 슬롯 프레셔 임계값을 지정할 수 있습니다. 시스템 부하가 임계값을 초과하면 새 태스크는 강제 삽입 대신 자동으로 큐에 들어가 에이전트 간 리소스 경쟁을 방지합니다:

```json
{
  "dispatch": {
    "maxSlots": 4,
    "slotPressureThreshold": 0.8
  }
}
```

### Partial-Done 상태

장시간 실행 태스크에서 `partial-done` 중간 상태를 지원합니다. 에이전트가 일부 작업 완료 후 진행 상황을 보고할 수 있으며, TaskBoard에 완료율이 표시되어 태스크가 정지했는지 진행 중인지 알 수 있습니다.

### Worktree 데이터 보호

여러 에이전트가 Git worktree를 사용하여 병렬 개발할 때, 명확한 데이터 격리 보호가 추가되었습니다. 각 에이전트의 작업 디렉토리는 독립적으로 유지되어, 의도치 않은 덮어쓰기나 다른 에이전트 상태를 오염시키는 머지 컨플릭트가 발생하지 않습니다.

### GitLab MR 지원

GitHub PR 외에도 GitLab Merge Request 워크플로를 지원합니다. `tetora pr create` 명령이 리모트 유형을 자동 감지하여 GitHub CLI 또는 GitLab CLI를 적절히 호출해 MR을 생성합니다.

## 설치 / 업그레이드

### 신규 설치

```bash
curl -fsSL https://tetora.dev/install.sh | bash
```

단일 바이너리, 외부 의존성 없음. macOS, Linux, Windows 모두 지원.

### 이전 버전에서 업그레이드

```bash
tetora upgrade
```

최신 버전을 자동으로 다운로드하고, 바이너리를 교체하고, 데몬을 재시작합니다. 업그레이드 중 실행 중인 태스크는 중단되지 않습니다.

> **팁:** 업그레이드 전에 장시간 실행 중인 워크플로가 없는지 확인하는 것을 권장합니다. `tetora status`로 활성 태스크를 확인하세요.

## 다음 단계: v2.2 로드맵

v2.1.0 출시 후, 개발 중점은 v2.2의 두 가지 테마 모듈로 이동합니다:

### Financial Module

개인 및 소기업을 위한 재무 자동화: 수입/지출 추적, 보고서 생성, 예산 모니터링. 일반적인 회계 API(freee, Money Forward 등)와의 통합 예정.

### Nutrition Module

건강 및 식이 추적: 식사 기록, 영양 분석, 목표 설정. Claude가 영양 어드바이저로서 식습관을 기반으로 맞춤형 조언을 제공합니다.

두 모듈 모두 Store에 Workflow 템플릿으로 공개될 예정으로, 처음부터 설정하지 않아도 바로 임포트하여 사용할 수 있습니다.

## 지금 v2.1.0으로 업그레이드

단일 바이너리, 의존성 없음. macOS / Linux / Windows.

```bash
tetora upgrade
```

[릴리즈 노트 보기](https://github.com/TakumaLee/Tetora/releases)
