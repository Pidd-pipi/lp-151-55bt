import { useEffect, useState } from 'react'
import { Card, Space, Typography, Button, Input, List, message, Tag, Avatar, Popconfirm, Empty } from 'antd'
import { LikeOutlined, CommentOutlined, EyeOutlined, RollbackOutlined } from '@ant-design/icons'
import { useParams, useNavigate } from 'react-router-dom'
import { ApiError, request } from '../api/client'
import type { Comment, PageResult, Post } from '../types'
import { getIdentity } from '../utils/storage'

export default function PostDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [post, setPost] = useState<Post | null>(null)
  const [comments, setComments] = useState<Comment[]>([])
  const [commentText, setCommentText] = useState('')
  const [notFound, setNotFound] = useState(false)
  const me = getIdentity()

  const load = async () => {
    try {
      const postData = await request<Post>('get', `/posts/${id}`)
      setPost(postData)
      const commentData = await request<PageResult<Comment>>('get', `/posts/${id}/comments`, { page: 1, page_size: 20 })
      setComments(commentData.items)
    } catch (e) {
      // 帖子被撤回或不存在时详情不再可访问
      if (e instanceof ApiError && e.status === 404) {
        setNotFound(true)
        return
      }
      message.error((e as Error).message)
    }
  }

  useEffect(() => {
    load()
  }, [id])

  const likePost = async () => {
    if (!getIdentity()) {
      message.warning('请先创建匿名身份')
      return
    }
    try {
      await request('post', '/likes/toggle', { targetType: 'post', targetId: Number(id) })
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const likeComment = async (commentId: number) => {
    if (!getIdentity()) {
      message.warning('请先创建匿名身份')
      return
    }
    try {
      await request('post', '/likes/toggle', { targetType: 'comment', targetId: commentId })
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const submitComment = async () => {
    if (!getIdentity()) {
      message.warning('请先创建匿名身份')
      return
    }
    if (!commentText.trim()) return
    try {
      await request('post', '/comments', { postId: Number(id), content: commentText.trim() })
      setCommentText('')
      message.success('评论已发布')
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const withdrawPost = async () => {
    try {
      await request('post', `/posts/${id}/withdraw`)
      message.success('帖子已撤回')
      navigate('/')
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        message.info('该帖子已撤回，无需重复操作')
        navigate('/')
        return
      }
      message.error((e as Error).message)
    }
  }

  const withdrawComment = async (commentId: number) => {
    try {
      await request('post', `/comments/${commentId}/withdraw`)
      message.success('评论已撤回')
      load()
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        message.info('该评论已撤回，无需重复操作')
        load()
        return
      }
      message.error((e as Error).message)
    }
  }

  if (notFound) {
    return (
      <Card>
        <Empty description="帖子不存在或已被作者撤回">
          <Button type="primary" onClick={() => navigate('/')}>返回首页</Button>
        </Empty>
      </Card>
    )
  }

  if (!post) return <Typography.Text>加载中...</Typography.Text>

  return (
    <div>
      <Button type="link" onClick={() => navigate(-1)}>返回</Button>
      <Card className="post-card">
        <Space align="start">
          <Avatar size={48} src={post.avatar} />
          <div>
            <Space>
              <Typography.Text strong>{post.nickname}</Typography.Text>
              <Typography.Text type="secondary">{post.createdAt}</Typography.Text>
            </Space>
            {post.title && <Typography.Title level={4} style={{ margin: '8px 0' }}>{post.title}</Typography.Title>}
            <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{post.content}</Typography.Paragraph>
            {post.images?.length > 0 && (
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', margin: '8px 0' }}>
                {post.images.map((url, idx) => (
                  <img key={idx} src={url} alt={`${post.nickname}-${idx}`} style={{ width: 160, height: 160, objectFit: 'cover', borderRadius: 8 }} />
                ))}
              </div>
            )}
            {post.tags.map((tag) => (
              <Tag key={tag.id} color="blue">{tag.name}</Tag>
            ))}
            <Space size="large" style={{ marginTop: 8 }}>
              <Button type={post.liked ? 'primary' : 'text'} icon={<LikeOutlined />} onClick={likePost}>
                {post.likeCount}
              </Button>
              <Typography.Text type="secondary"><CommentOutlined /> {post.commentCount}</Typography.Text>
              <Typography.Text type="secondary"><EyeOutlined /> {post.viewCount}</Typography.Text>
              {me?.id === post.identityId && (
                <Popconfirm
                  title="确认撤回这条帖子吗？"
                  description="撤回后将从所有列表移除，且无法恢复"
                  okText="确认撤回"
                  cancelText="取消"
                  onConfirm={withdrawPost}
                >
                  <Button type="text" danger icon={<RollbackOutlined />}>撤回</Button>
                </Popconfirm>
              )}
            </Space>
          </div>
        </Space>
      </Card>

      <Card title={`评论 (${comments.length})`} style={{ marginTop: 16 }}>
        <Space.Compact style={{ width: '100%', marginBottom: 16 }}>
          <Input.TextArea value={commentText} onChange={(e) => setCommentText(e.target.value)} placeholder="写下你的匿名评论..." autoSize={{ minRows: 2, maxRows: 4 }} />
        </Space.Compact>
        <Button type="primary" onClick={submitComment} style={{ marginTop: 8 }}>发表评论</Button>
        <List
          style={{ marginTop: 16 }}
          dataSource={comments}
          locale={{ emptyText: '暂无评论' }}
          renderItem={(item) => (
            <List.Item
              actions={[
                <Button key="like" type="text" icon={<LikeOutlined />} onClick={() => likeComment(item.id)}>
                  {item.likeCount}
                </Button>,
                ...(me?.id === item.identityId
                  ? [
                      <Popconfirm
                        key="withdraw"
                        title="确认撤回这条评论吗？"
                        okText="确认撤回"
                        cancelText="取消"
                        onConfirm={() => withdrawComment(item.id)}
                      >
                        <Button type="text" danger size="small">撤回</Button>
                      </Popconfirm>,
                    ]
                  : []),
              ]}
            >
              <List.Item.Meta
                avatar={<Avatar src={item.avatar} />}
                title={<Space><Typography.Text strong>{item.nickname}</Typography.Text><Typography.Text type="secondary">{item.createdAt}</Typography.Text></Space>}
                description={<Typography.Paragraph style={{ marginBottom: 0 }}>{item.content}</Typography.Paragraph>}
              />
            </List.Item>
          )}
        />
      </Card>
    </div>
  )
}
