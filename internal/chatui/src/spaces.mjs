export function spaceChatGroups(spaces = [], chats = []) {
  const pinned = chats.filter(chat => chat.pinned);
  const local = chats.filter(chat => !chat.pinned && chat.space_id === 'local');
  const groups = spaces.filter(space => space.id !== 'local').map(space => ({
    space, chats: chats.filter(chat => !chat.pinned && chat.space_id === space.id),
  }));
  return { pinned, local, groups };
}

export function spaceSelection(spaces, id = 'local') {
  return spaces.find(space => space.id === id) || spaces.find(space => space.id === 'local') || null;
}

export function publicationMessage(space) {
  return space?.source !== 'local'
    ? '目标为外部空间。对话的公开文本将向该聊天开放；工作区不变，后续操作按目标空间和发起人权限执行。'
    : '工作区不变，后续操作按目标空间权限执行。';
}
